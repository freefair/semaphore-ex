package haresilience

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type commandRunner struct {
	repositoryDir string
	composeFile   string
	projectName   string
	environment   map[string]string
}

func (r *commandRunner) compose(ctx context.Context, arguments ...string) (string, error) {
	args := []string{"compose", "--project-name", r.projectName, "--file", r.composeFile}
	args = append(args, arguments...)
	return r.run(ctx, "docker", args...)
}

func (r *commandRunner) docker(ctx context.Context, arguments ...string) (string, error) {
	return r.run(ctx, "docker", arguments...)
}

func (r *commandRunner) run(ctx context.Context, name string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = r.repositoryDir
	command.Env = os.Environ()
	for key, value := range r.environment {
		command.Env = append(command.Env, key+"="+value)
	}
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return output.String(), fmt.Errorf("%s %s: %w\n%s", name, strings.Join(arguments, " "), err, output.String())
	}
	return strings.TrimSpace(output.String()), nil
}

func (r *commandRunner) port(ctx context.Context, service string, containerPort string) (string, error) {
	output, err := r.compose(ctx, "port", service, containerPort)
	if err != nil {
		return "", err
	}
	if output == "" {
		return "", fmt.Errorf("compose returned no published port for %s:%s", service, containerPort)
	}
	return "http://" + output, nil
}

func (r *commandRunner) sql(ctx context.Context, query string) (string, error) {
	return r.compose(ctx, "exec", "--no-TTY", "postgres", "psql", "--username", "semaphore", "--dbname", "semaphore", "--tuples-only", "--no-align", "--command", query)
}
