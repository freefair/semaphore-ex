package docker

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	moby "github.com/moby/moby/client"
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
	Name         string
	Image        string
	User         string
	Command      []string
	Environment  []string
	WorkingDir   string
	Labels       map[string]string
	Network      string
	NanoCPUs     int64
	Memory       int64
	Privileged   bool
	VolumeMounts []VolumeMount
	BindMounts   []string
	Tmpfs        []string
}

type DockerClient interface {
	PrepareImage(context.Context, string, PullPolicy) error
	CreateVolume(context.Context, string, map[string]string) (string, error)
	CreateContainer(context.Context, ContainerSpec) (string, error)
	StartContainer(context.Context, string) error
	CopyArchive(context.Context, string, string, io.Reader) error
	Exec(context.Context, string, []string, io.Writer, io.Writer) (int, error)
	StopContainer(context.Context, string, time.Duration) error
	KillContainer(context.Context, string) error
	RemoveContainer(context.Context, string) error
	RemoveVolume(context.Context, string) error
}

type mobyClient struct {
	client *moby.Client
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
	return &mobyClient{client: client}, nil
}

func (c *mobyClient) PrepareImage(ctx context.Context, image string, policy PullPolicy) error {
	switch policy {
	case PullAlways:
		return c.pullImage(ctx, image)
	case PullIfNotPresent:
		if _, err := c.client.ImageInspect(ctx, image); err == nil {
			return nil
		} else if !errdefs.IsNotFound(err) {
			return fmt.Errorf("inspecting Docker image: %w", err)
		}
		return c.pullImage(ctx, image)
	case PullNever:
		if _, err := c.client.ImageInspect(ctx, image); err != nil {
			return fmt.Errorf("Docker image %q is unavailable with pull policy never: %w", image, err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported Docker pull policy %q", policy)
	}
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
	if len(spec.BindMounts) != 0 {
		return "", fmt.Errorf("Docker task containers do not support host bind mounts")
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
			NetworkMode: container.NetworkMode(spec.Network),
			Privileged:  spec.Privileged,
			Mounts:      mounts,
			Tmpfs:       tmpfs,
			Init:        &init,
			Resources: container.Resources{
				NanoCPUs: spec.NanoCPUs,
				Memory:   spec.Memory,
			},
		},
	})
	if err != nil {
		return "", err
	}
	return result.ID, nil
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

func (c *mobyClient) RemoveContainer(ctx context.Context, containerID string) error {
	_, err := c.client.ContainerRemove(ctx, containerID, moby.ContainerRemoveOptions{Force: true})
	return err
}

func (c *mobyClient) RemoveVolume(ctx context.Context, volume string) error {
	_, err := c.client.VolumeRemove(ctx, volume, moby.VolumeRemoveOptions{Force: true})
	return err
}
