package docker

import (
	"context"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/strslice"
	moby "github.com/moby/moby/client"
	"github.com/semaphoreui/semaphore/db"
)

const containerBundlePath = "/semaphore/bundle"

type VolumeMount struct {
	Source   string
	Target   string
	ReadOnly bool
}

// ContainerSpec is deliberately narrower than Docker's create request. It has
// no host-path, socket, device, capability, port, or task-controlled fields.
type ContainerSpec struct {
	Name                string
	Image               string
	User                string
	Command             []string
	Environment         []string
	WorkingDir          string
	Labels              map[string]string
	Network             string
	NanoCPUs            int64
	Memory              int64
	Privileged          bool
	ReadOnlyRootFS      bool
	NoNewPrivileges     bool
	DropAllCapabilities bool
	PrivateNamespaces   bool
	PidsLimit           int64
	SeccompProfile      string
	AppArmorProfile     string
	VolumeMounts        []VolumeMount
	BindMounts          []string
	Tmpfs               []string
}

type DockerClient interface {
	ResolveImage(context.Context, string, ImageRole, db.DockerExecutionPolicy) (ResolvedImage, error)
	CreateVolume(context.Context, string, map[string]string) (string, error)
	CreateContainer(context.Context, ContainerSpec) (string, error)
	StartContainer(context.Context, string) error
	CopyArchive(context.Context, string, string, io.Reader) error
	Exec(context.Context, string, []string, io.Writer, io.Writer) (int, error)
	StopContainer(context.Context, string, time.Duration) error
	KillContainer(context.Context, string) error
	InspectContainer(context.Context, string) (ContainerState, error)
	ListManagedResources(context.Context, int) ([]ManagedResource, error)
	RemoveContainer(context.Context, string) error
	RemoveVolume(context.Context, string) error
}

type ManagedResourceKind string

const (
	ManagedContainer ManagedResourceKind = "container"
	ManagedVolume    ManagedResourceKind = "volume"
)

// ManagedResource is the narrow, bounded daemon view consumed by the
// reconciliation parser. Raw Docker inspect payloads and errors never cross
// this boundary.
type ManagedResource struct {
	Kind       ManagedResourceKind
	ID         string
	Name       string
	Labels     map[string]string
	Running    bool
	StateKnown bool
}

const (
	maxDockerReconciliationPageSize = 100
	managedReconciliationTimeout    = 10 * time.Second
)

// ContainerState intentionally exposes only reconciliation evidence. Docker's
// full inspect payload may include environment, mounts, and daemon details and
// must never leave this package.
type ContainerState struct {
	Exists  bool
	Running bool
	ID      string
	Name    string
	Labels  map[string]string
}

type ImageRole string

const (
	ImageRoleHelper ImageRole = "helper"
	ImageRoleTask   ImageRole = "task"
)

type ResolvedImage struct {
	RequestedReference string
	ResolvedReference  string
	Digest             string
	Source             string
	SizeBytes          int64
}

type mobyClient struct {
	client     *moby.Client
	pullPolicy PullPolicy
}

func newMobyClient(cfg config) (DockerClient, error) {
	options := []moby.Opt{moby.FromEnv, moby.WithAPIVersionNegotiation()}
	if cfg.host != "" {
		options = append(options, moby.WithHost(cfg.host))
	}
	if cfg.tlsVerify {
		caFile, certFile, keyFile := "", "", ""
		if cfg.certPath != "" {
			caFile = filepath.Join(cfg.certPath, "ca.pem")
			certFile = filepath.Join(cfg.certPath, "cert.pem")
			keyFile = filepath.Join(cfg.certPath, "key.pem")
		}
		options = append(options, moby.WithTLSClientConfig(caFile, certFile, keyFile))
	}
	client, err := moby.NewClientWithOpts(options...)
	if err != nil {
		return nil, fmt.Errorf("creating Docker API client: %w", err)
	}
	return &mobyClient{client: client, pullPolicy: cfg.pullPolicy}, nil
}

func (c *mobyClient) ResolveImage(ctx context.Context, requested string, _ ImageRole, policy db.DockerExecutionPolicy) (ResolvedImage, error) {
	if err := policy.Validate(); err != nil {
		return ResolvedImage{}, err
	}
	pullCtx, cancel := context.WithTimeout(ctx, time.Duration(policy.PullTimeoutSeconds)*time.Second)
	defer cancel()
	source := "local"
	switch c.pullPolicy {
	case PullAlways:
		if err := c.pullImage(pullCtx, requested); err != nil {
			return ResolvedImage{}, err
		}
		source = "pulled"
	case PullIfNotPresent:
		if _, err := c.client.ImageInspect(pullCtx, requested); err == nil {
			break
		} else if !errdefs.IsNotFound(err) {
			return ResolvedImage{}, fmt.Errorf("inspecting Docker image: %w", err)
		}
		if err := c.pullImage(pullCtx, requested); err != nil {
			return ResolvedImage{}, err
		}
		source = "pulled"
	case PullNever:
		if _, err := c.client.ImageInspect(pullCtx, requested); err != nil {
			return ResolvedImage{}, fmt.Errorf("Docker image is unavailable with pull policy never: %w", err)
		}
	default:
		return ResolvedImage{}, fmt.Errorf("unsupported Docker pull policy %q", c.pullPolicy)
	}
	inspected, err := c.client.ImageInspect(pullCtx, requested)
	if err != nil {
		return ResolvedImage{}, fmt.Errorf("inspecting resolved Docker image: %w", err)
	}
	if inspected.Size <= 0 || inspected.Size > policy.MaxImageSizeBytes {
		return ResolvedImage{}, db.DockerPolicyViolationError{Rule: db.DockerPolicyRuleResourceDenied}
	}
	for _, candidate := range inspected.RepoDigests {
		if imageRepository(candidate) == imageRepository(requested) && slices.Contains(policy.AllowedImages, candidate) {
			return ResolvedImage{RequestedReference: requested, ResolvedReference: candidate, Digest: candidate[strings.LastIndex(candidate, "@"):], Source: source, SizeBytes: inspected.Size}, nil
		}
	}
	return ResolvedImage{}, db.DockerPolicyViolationError{Rule: db.DockerPolicyRuleImageDenied}
}

func imageRepository(reference string) string {
	if at := strings.Index(reference, "@"); at >= 0 {
		return reference[:at]
	}
	lastSlash := strings.LastIndex(reference, "/")
	if colon := strings.LastIndex(reference, ":"); colon > lastSlash {
		return reference[:colon]
	}
	return reference
}

func (c *mobyClient) pullImage(ctx context.Context, image string) error {
	response, err := c.client.ImagePull(ctx, image, moby.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pulling Docker image %q: %w", image, err)
	}
	defer response.Close() //nolint:errcheck
	if err := response.Wait(ctx); err != nil {
		return fmt.Errorf("pulling Docker image %q: %w", image, err)
	}
	return nil
}

func (c *mobyClient) CreateVolume(ctx context.Context, name string, labels map[string]string) (string, error) {
	result, err := c.client.VolumeCreate(ctx, moby.VolumeCreateOptions{Name: name, Labels: labels})
	if err != nil {
		return "", err
	}
	return result.Volume.Name, nil
}

func (c *mobyClient) CreateContainer(ctx context.Context, spec ContainerSpec) (string, error) {
	if err := validateContainerSpec(spec); err != nil {
		return "", err
	}
	mounts := make([]mount.Mount, len(spec.VolumeMounts))
	for index, volume := range spec.VolumeMounts {
		mounts[index] = mount.Mount{Type: mount.TypeVolume, Source: volume.Source, Target: volume.Target, ReadOnly: volume.ReadOnly}
	}
	tmpfs := make(map[string]string, len(spec.Tmpfs))
	for _, target := range spec.Tmpfs {
		tmpfs[target] = "rw,nosuid,nodev,uid=65534,gid=0,mode=0750"
	}
	init := true
	pidsLimit := spec.PidsLimit
	result, err := c.client.ContainerCreate(ctx, moby.ContainerCreateOptions{
		Name: spec.Name,
		Config: &container.Config{
			Image:      spec.Image,
			User:       spec.User,
			Cmd:        slices.Clone(spec.Command),
			Env:        slices.Clone(spec.Environment),
			WorkingDir: spec.WorkingDir,
			Labels:     spec.Labels,
		},
		HostConfig: &container.HostConfig{
			NetworkMode:    container.NetworkMode(spec.Network),
			Privileged:     false,
			Mounts:         mounts,
			Tmpfs:          tmpfs,
			Init:           &init,
			ReadonlyRootfs: spec.ReadOnlyRootFS,
			SecurityOpt:    securityOptions(spec),
			CapDrop:        strslice.StrSlice{"ALL"},
			IpcMode:        container.IpcMode("private"),
			// Empty is Docker's valid private default for PID, UTS, and user namespaces.
			PidMode:    container.PidMode(""),
			UTSMode:    container.UTSMode(""),
			UsernsMode: container.UsernsMode(""),
			Resources: container.Resources{
				NanoCPUs:  spec.NanoCPUs,
				Memory:    spec.Memory,
				PidsLimit: &pidsLimit,
			},
		},
	})
	if err != nil {
		return "", err
	}
	return result.ID, nil
}

func securityOptions(spec ContainerSpec) []string {
	options := []string{"no-new-privileges:true"}
	if spec.SeccompProfile != "" && spec.SeccompProfile != "default" {
		options = append(options, "seccomp="+spec.SeccompProfile)
	}
	if spec.AppArmorProfile != "" && spec.AppArmorProfile != "docker-default" {
		options = append(options, "apparmor="+spec.AppArmorProfile)
	}
	return options
}

func validateContainerSpec(spec ContainerSpec) error {
	if spec.Privileged {
		return db.DockerPolicyViolationError{Rule: db.DockerPolicyRulePrivilegeDenied}
	}
	if len(spec.BindMounts) != 0 {
		return db.DockerPolicyViolationError{Rule: db.DockerPolicyRuleBindMountDenied}
	}
	if !spec.ReadOnlyRootFS {
		return db.DockerPolicyViolationError{Rule: db.DockerPolicyRuleReadonlyRequired}
	}
	if !spec.NoNewPrivileges || !spec.DropAllCapabilities {
		return db.DockerPolicyViolationError{Rule: db.DockerPolicyRuleCapabilityDenied}
	}
	if !spec.PrivateNamespaces || spec.Network == "host" || strings.HasPrefix(spec.Network, "container:") {
		return db.DockerPolicyViolationError{Rule: db.DockerPolicyRuleNamespaceRequired}
	}
	if spec.User != "65534:0" {
		return db.DockerPolicyViolationError{Rule: db.DockerPolicyRuleIdentityDenied}
	}
	if spec.NanoCPUs <= 0 || spec.Memory <= 0 || spec.PidsLimit <= 0 {
		return db.DockerPolicyViolationError{Rule: db.DockerPolicyRuleResourceDenied}
	}
	return nil
}

func (c *mobyClient) StartContainer(ctx context.Context, containerID string) error {
	_, err := c.client.ContainerStart(ctx, containerID, moby.ContainerStartOptions{})
	return err
}

func (c *mobyClient) CopyArchive(ctx context.Context, containerID string, destination string, archive io.Reader) error {
	if destination != containerBundlePath {
		return fmt.Errorf("unsupported Docker bundle destination %q", destination)
	}
	_, err := c.client.CopyToContainer(ctx, containerID, moby.CopyToContainerOptions{
		DestinationPath: destination,
		Content:         archive,
		CopyUIDGID:      true,
	})
	return err
}

func (c *mobyClient) Exec(ctx context.Context, containerID string, command []string, stdout io.Writer, stderr io.Writer) (int, error) {
	if !validStageCommand(command) {
		return 0, fmt.Errorf("unsupported Docker task command")
	}
	created, err := c.client.ExecCreate(ctx, containerID, moby.ExecCreateOptions{
		User:         "65534:0",
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          slices.Clone(command),
	})
	if err != nil {
		return 0, err
	}
	attached, err := c.client.ExecAttach(ctx, created.ID, moby.ExecAttachOptions{})
	if err != nil {
		return 0, err
	}
	defer attached.Close()
	if _, err := stdcopy.StdCopy(stdout, stderr, attached.Reader); err != nil {
		return 0, err
	}
	inspected, err := c.client.ExecInspect(ctx, created.ID, moby.ExecInspectOptions{})
	if err != nil {
		return 0, err
	}
	return inspected.ExitCode, nil
}

func validStageCommand(command []string) bool {
	if len(command) != 3 || command[0] != "/bin/sh" || command[1] != "/semaphore/bundle/run.sh" {
		return false
	}
	switch command[2] {
	case "bootstrap", "run", "plan", "apply":
		return true
	default:
		return false
	}
}

func (c *mobyClient) StopContainer(ctx context.Context, containerID string, grace time.Duration) error {
	seconds := int(grace / time.Second)
	_, err := c.client.ContainerStop(ctx, containerID, moby.ContainerStopOptions{Timeout: &seconds})
	return err
}

func (c *mobyClient) KillContainer(ctx context.Context, containerID string) error {
	_, err := c.client.ContainerKill(ctx, containerID, moby.ContainerKillOptions{Signal: "SIGKILL"})
	return err
}

func (c *mobyClient) InspectContainer(ctx context.Context, containerID string) (ContainerState, error) {
	inspected, err := c.client.ContainerInspect(ctx, containerID, moby.ContainerInspectOptions{})
	if errdefs.IsNotFound(err) {
		return ContainerState{Exists: false}, nil
	}
	if err != nil {
		return ContainerState{}, err
	}
	return ContainerState{Exists: true, Running: inspected.Container.State != nil && inspected.Container.State.Running, ID: inspected.Container.ID, Name: strings.TrimPrefix(inspected.Container.Name, "/"), Labels: maps.Clone(inspected.Container.Config.Labels)}, nil
}

func (c *mobyClient) ListManagedResources(ctx context.Context, runnerID int) ([]ManagedResource, error) {
	if runnerID <= 0 {
		return nil, fmt.Errorf("invalid Docker runner identity")
	}
	listCtx, cancel := context.WithTimeout(ctx, managedReconciliationTimeout)
	defer cancel()
	filter := make(moby.Filters).
		Add("label", "io.semaphore.managed=v1").
		Add("label", fmt.Sprintf("io.semaphore.runner-id=%d", runnerID))
	containers, err := c.client.ContainerList(listCtx, moby.ContainerListOptions{All: true, Filters: filter})
	if err != nil {
		return nil, fmt.Errorf("listing managed Docker containers: %w", err)
	}
	volumes, err := c.client.VolumeList(listCtx, moby.VolumeListOptions{Filters: filter})
	if err != nil {
		return nil, fmt.Errorf("listing managed Docker volumes: %w", err)
	}
	resources := make([]ManagedResource, 0, len(containers.Items)+len(volumes.Items))
	for _, summary := range containers.Items {
		inspected, inspectErr := c.client.ContainerInspect(listCtx, summary.ID, moby.ContainerInspectOptions{})
		if errdefs.IsNotFound(inspectErr) {
			continue
		}
		if inspectErr != nil {
			return nil, fmt.Errorf("inspecting managed Docker container: %w", inspectErr)
		}
		name := strings.TrimPrefix(inspected.Container.Name, "/")
		resources = append(resources, ManagedResource{Kind: ManagedContainer, ID: inspected.Container.ID, Name: name, Labels: maps.Clone(inspected.Container.Config.Labels), Running: inspected.Container.State != nil && inspected.Container.State.Running, StateKnown: inspected.Container.State != nil})
	}
	for _, summary := range volumes.Items {
		inspected, inspectErr := c.client.VolumeInspect(listCtx, summary.Name, moby.VolumeInspectOptions{})
		if errdefs.IsNotFound(inspectErr) {
			continue
		}
		if inspectErr != nil {
			return nil, fmt.Errorf("inspecting managed Docker volume: %w", inspectErr)
		}
		resources = append(resources, ManagedResource{Kind: ManagedVolume, ID: inspected.Volume.Name, Name: inspected.Volume.Name, Labels: maps.Clone(inspected.Volume.Labels), StateKnown: true})
	}
	return resources, nil
}

func (c *mobyClient) RemoveContainer(ctx context.Context, containerID string) error {
	_, err := c.client.ContainerRemove(ctx, containerID, moby.ContainerRemoveOptions{Force: true})
	return err
}

func (c *mobyClient) RemoveVolume(ctx context.Context, volume string) error {
	_, err := c.client.VolumeRemove(ctx, volume, moby.VolumeRemoveOptions{Force: true})
	return err
}
