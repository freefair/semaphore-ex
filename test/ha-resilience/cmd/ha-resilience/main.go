package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	haresilience "github.com/semaphoreui/semaphore/test/ha-resilience"
)

func main() {
	repository := flag.String("repository", ".", "repository root")
	reportPath := flag.String("report", "dist/ha-resilience/report.json", "machine-readable result path")
	project := flag.String("project", fmt.Sprintf("semaphore-ha-%d", time.Now().UnixNano()), "isolated Compose project")
	keep := flag.Bool("keep", false, "keep the disposable Compose environment after the run")
	flag.Parse()

	repositoryPath, err := filepath.Abs(*repository)
	if err != nil {
		fail(err)
	}
	resultPath := *reportPath
	if !filepath.IsAbs(resultPath) {
		resultPath = filepath.Join(repositoryPath, resultPath)
	}
	config := haresilience.Config{
		RepositoryDir:    repositoryPath,
		ComposeFile:      filepath.Join(repositoryPath, "test", "ha-resilience", "compose.yaml"),
		ProjectName:      *project,
		NetworkName:      *project + "-network",
		ReportPath:       resultPath,
		ServerAImage:     requiredEnvironment("SEMAPHORE_HA_SERVER_A_IMAGE"),
		ServerBImage:     requiredEnvironment("SEMAPHORE_HA_SERVER_B_IMAGE"),
		ReplacementImage: requiredEnvironment("SEMAPHORE_HA_REPLACEMENT_IMAGE"),
		RunnerImage:      requiredEnvironment("SEMAPHORE_HA_RUNNER_IMAGE"),
		KeepEnvironment:  *keep,
	}
	harness, err := haresilience.NewHarness(config)
	if err != nil {
		fail(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := harness.Run(ctx); err != nil {
		fail(err)
	}
	fmt.Printf("HA resilience verification passed; report: %s\n", resultPath)
}

func requiredEnvironment(name string) string {
	value := os.Getenv(name)
	if value == "" {
		fail(fmt.Errorf("required environment variable %s is empty", name))
	}
	return value
}

func fail(err error) {
	_, _ = fmt.Fprintf(os.Stderr, "HA resilience verification failed: %v\n", err)
	os.Exit(1)
}
