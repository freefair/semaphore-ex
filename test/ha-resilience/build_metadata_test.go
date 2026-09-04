package haresilience

import (
	"os"
	"strings"
	"testing"
)

func TestCleanRoomServerBuildPassesVCSMetadataToEveryTask(t *testing.T) {
	content, err := os.ReadFile("../../deployment/docker/server/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}

	dockerfile := string(content)
	for _, taskName := range []string{"deps:be", "deps:fe", "build:fe", "build:be"} {
		block := dockerTaskInvocation(dockerfile, taskName)
		if block == "" {
			t.Fatalf("task %s invocation not found", taskName)
		}
		for _, variable := range []string{"CORE_REVISION", "ENHANCED_REVISION", "SOURCE_DATE_EPOCH"} {
			if !strings.Contains(block, variable+"=") {
				t.Errorf("task %s does not receive %s", taskName, variable)
			}
		}
	}
}

func dockerTaskInvocation(dockerfile string, taskName string) string {
	lines := strings.Split(dockerfile, "\n")
	for index, line := range lines {
		if !strings.Contains(line, "task "+taskName+" \\") {
			continue
		}
		block := make([]string, 0, 8)
		for _, invocationLine := range lines[index:] {
			block = append(block, invocationLine)
			trimmed := strings.TrimSpace(invocationLine)
			if strings.Contains(trimmed, "&& \\") || !strings.HasSuffix(trimmed, "\\") {
				break
			}
		}
		return strings.Join(block, "\n")
	}
	return ""
}
