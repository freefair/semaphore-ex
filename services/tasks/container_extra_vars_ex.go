package tasks

import (
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/semaphoreui/semaphore/db"
)

const (
	containerOpenExtraVarsPath       = "credentials/extra-vars.yml"
	containerSecretExtraVarsPath     = "credentials/secret-vars.yml"
	containerSecretVaultPasswordPath = "credentials/secret-vars-vault-password"
)

// prepareContainerExtraVars copies only the files created for this task into
// the private bundle. Host paths are never carried into a container command.
func (t *LocalExecutor) prepareContainerExtraVars(args map[string][]string) ([]containerBundleFile, error) {
	if t.Template.App != db.AppAnsible || (t.extraVarsFile == "" && t.secretVarsFile == "") {
		return nil, nil
	}

	files := make([]containerBundleFile, 0, 3)
	paths := make(map[string]string, 2)
	if t.extraVarsFile != "" {
		open, err := readContainerExtraVarsFile(t.extraVarsFile)
		if err != nil {
			return nil, fmt.Errorf("reading task extra vars: %w", err)
		}
		files = append(files, containerBundleFile{
			name: containerOpenExtraVarsPath,
			mode: 0o600,
			data: open,
		})
		paths[t.extraVarsFile] = path.Join(containerBundlePath, containerOpenExtraVarsPath)
	}
	if t.secretVarsFile != "" {
		secret, readErr := readContainerExtraVarsFile(t.secretVarsFile)
		if readErr != nil {
			return nil, fmt.Errorf("reading task secret vars: %w", readErr)
		}
		if t.secretVaultID == "" || t.secretVaultPassword == "" {
			return nil, fmt.Errorf("task secret vars have no vault password")
		}
		files = append(files,
			containerBundleFile{name: containerSecretExtraVarsPath, mode: 0o600, data: secret},
			containerBundleFile{name: containerSecretVaultPasswordPath, mode: 0o600, data: []byte(t.secretVaultPassword)},
		)
		paths[t.secretVarsFile] = path.Join(containerBundlePath, containerSecretExtraVarsPath)
	}

	for stage, stageArgs := range args {
		remapped, remapErr := remapContainerExtraVarsArgs(stageArgs, paths, t.secretVaultID, t.secretVarsFile != "")
		if remapErr != nil {
			return nil, fmt.Errorf("remapping %s container extra vars: %w", stage, remapErr)
		}
		args[stage] = remapped
	}
	return files, nil
}

func readContainerExtraVarsFile(filename string) ([]byte, error) {
	before, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("not a regular file")
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, fmt.Errorf("file changed while preparing bundle")
	}
	return io.ReadAll(file)
}

func remapContainerExtraVarsArgs(args []string, paths map[string]string, vaultID string, hasSecretVars bool) ([]string, error) {
	result := append([]string(nil), args...)
	for index := 0; index < len(result); index++ {
		arg := result[index]
		if arg == "--extra-vars" || arg == "-e" {
			if index+1 == len(result) {
				return nil, fmt.Errorf("missing extra vars value")
			}
			replacement, err := remapContainerExtraVarsPath(result[index+1], paths)
			if err != nil {
				return nil, err
			}
			result[index+1] = replacement
			index++
			continue
		}
		if strings.HasPrefix(arg, "--extra-vars=") || strings.HasPrefix(arg, "-e=") {
			prefix := "--extra-vars="
			if strings.HasPrefix(arg, "-e=") {
				prefix = "-e="
			}
			replacement, err := remapContainerExtraVarsPath(strings.TrimPrefix(arg, prefix), paths)
			if err != nil {
				return nil, err
			}
			result[index] = prefix + replacement
			continue
		}
		if hasSecretVars && arg == "--vault-id="+vaultID+"@prompt" {
			result[index] = "--vault-id=" + vaultID + "@" + path.Join(containerBundlePath, containerSecretVaultPasswordPath)
		}
	}
	return result, nil
}

func remapContainerExtraVarsPath(value string, paths map[string]string) (string, error) {
	if !strings.HasPrefix(value, "@") {
		return value, nil
	}
	filename := strings.TrimPrefix(value, "@")
	replacement, ok := paths[filename]
	if ok {
		return "@" + replacement, nil
	}
	if filename == "" {
		return "", fmt.Errorf("unapproved extra vars file")
	}
	if path.IsAbs(filename) {
		return value, nil
	}
	cleanName := path.Clean(filename)
	if cleanName == "." || cleanName == ".." {
		return "", fmt.Errorf("unapproved extra vars file")
	}
	if strings.HasPrefix(cleanName, "../") {
		// This path intentionally resolves from the task image's working
		// directory. It is not a host path and must not be rewritten.
		return value, nil
	}
	// Repository-relative paths are copied to the workspace and retain their
	// existing container rewrite. Generated runner paths took precedence above.
	return "@" + path.Join(containerWorkspacePath, cleanName), nil
}
