package haresilience

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	RepositoryDir    string
	ComposeFile      string
	ProjectName      string
	NetworkName      string
	ReportPath       string
	ServerAImage     string
	ServerBImage     string
	ReplacementImage string
	RunnerImage      string
	KeepEnvironment  bool
}

type Harness struct {
	config              Config
	commands            *commandRunner
	api                 *apiClient
	report              Report
	adminPass           string
	proxyURL            string
	serverAURL          string
	serverBURL          string
	projectID           int
	templateID          int
	scheduleID          int
	recoveryTaskID      int
	recoveryOwnerNodeID string
	recoveryOwnerBootID string
	workflowID          int
	runID               int
	acceptedIDs         []int
}

func NewHarness(config Config) (*Harness, error) {
	api, err := newAPIClient()
	if err != nil {
		return nil, err
	}
	adminPassword, err := randomHex(24)
	if err != nil {
		return nil, err
	}
	dbPassword, err := randomHex(24)
	if err != nil {
		return nil, err
	}
	encryptionKey, err := randomBase64(32)
	if err != nil {
		return nil, err
	}
	runnerToken, err := randomHex(24)
	if err != nil {
		return nil, err
	}
	environment := map[string]string{
		"SEMAPHORE_HA_DB_PASSWORD":               dbPassword,
		"SEMAPHORE_HA_ADMIN_PASSWORD":            adminPassword,
		"SEMAPHORE_HA_ENCRYPTION_KEY":            encryptionKey,
		"SEMAPHORE_HA_RUNNER_REGISTRATION_TOKEN": runnerToken,
		"SEMAPHORE_HA_NETWORK":                   config.NetworkName,
		"SEMAPHORE_HA_SERVER_A_IMAGE":            config.ServerAImage,
		"SEMAPHORE_HA_SERVER_B_IMAGE":            config.ServerBImage,
		"SEMAPHORE_HA_RUNNER_IMAGE":              config.RunnerImage,
	}
	harness := &Harness{
		config: config, api: api, adminPass: adminPassword,
		commands: &commandRunner{repositoryDir: config.RepositoryDir, composeFile: config.ComposeFile, projectName: config.ProjectName, environment: environment},
	}
	harness.report = Report{
		SchemaVersion: 1, Result: "running", StartedAt: time.Now().UTC(),
		Topology: Topology{
			DatabaseImage: "postgres@sha256:95206741a5b214807675e14165369d05b93a9cf692223b616d07cca227e74b0b",
			RedisImage:    "redis@sha256:becdda6c7f4b3fb42e42fd7f120bbf5c54c4caaaf16f26da24e4563d2c1f0576",
			ProxyImage:    "nginx@sha256:db35bfc6b2951e7f8a72db5db120288c127ffaeeb4a6d4b95a26fead017d5913",
			InitialImages: []string{config.ServerAImage, config.ServerBImage}, ReplacementImage: config.ReplacementImage,
			RunnerImage: config.RunnerImage, NodeIDs: []string{"server-a", "server-b"},
		},
		UpgradePolicy: UpgradePolicy{
			Edition: "exact", Protocol: "exact", Schema: "exact", Capabilities: "required subset",
			ApplicationVersion: "may differ", Build: "may differ",
		},
	}
	return harness, nil
}

func (h *Harness) Run(ctx context.Context) (runErr error) {
	defer func() {
		h.report.CompletedAt = time.Now().UTC()
		if runErr != nil {
			h.report.Result = "failed"
		} else {
			h.report.Result = "passed"
			if err := h.report.Validate(); err != nil {
				runErr = err
				h.report.Result = "failed"
			}
		}
		if err := h.writeReport(); err != nil && runErr == nil {
			runErr = err
		}
		if !h.config.KeepEnvironment {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			_, _ = h.commands.compose(cleanupCtx, "down", "--volumes", "--remove-orphans")
		}
	}()
	if err := h.startTopology(ctx); err != nil {
		return err
	}
	if err := h.seed(ctx); err != nil {
		return err
	}
	auditBefore, err := h.sqlInt(ctx, "select count(*) from event;")
	if err != nil {
		return err
	}
	h.report.Invariants.AuditEventsBefore = auditBefore
	for _, scenario := range []func(context.Context) error{
		h.killNode, h.pauseNode, h.partitionNode, h.restartRedis, h.loseDatabase, h.reconnectRunner, h.rollingReplacement,
	} {
		if err := scenario(ctx); err != nil {
			return err
		}
	}
	if err := h.collectInvariants(ctx); err != nil {
		return err
	}
	return nil
}

func (h *Harness) startTopology(ctx context.Context) error {
	if _, err := h.commands.compose(ctx, "up", "--detach", "--wait", "--wait-timeout", "240", "postgres", "redis", "server-a", "server-b", "proxy", "runner"); err != nil {
		return err
	}
	return h.refreshURLs(ctx)
}

func (h *Harness) refreshURLs(ctx context.Context) error {
	var err error
	if h.proxyURL, err = h.commands.port(ctx, "proxy", "8080"); err != nil {
		return err
	}
	if h.serverAURL, err = h.commands.port(ctx, "server-a", "3000"); err != nil {
		return err
	}
	if h.serverBURL, err = h.commands.port(ctx, "server-b", "3000"); err != nil {
		return err
	}
	if err := h.api.login(ctx, h.proxyURL, h.adminPass); err != nil {
		return fmt.Errorf("login through proxy: %w", err)
	}
	return nil
}

func (h *Harness) sqlInt(ctx context.Context, query string) (int, error) {
	value, err := h.commands.sql(ctx, query)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("parse SQL count %q: %w", value, err)
	}
	return parsed, nil
}

func (h *Harness) writeReport() error {
	if err := os.MkdirAll(filepath.Dir(h.config.ReportPath), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(h.report, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return os.WriteFile(h.config.ReportPath, encoded, 0o644)
}

func randomHex(bytesCount int) (string, error) {
	buffer := make([]byte, bytesCount)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func randomBase64(bytesCount int) (string, error) {
	buffer := make([]byte, bytesCount)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buffer), nil
}
