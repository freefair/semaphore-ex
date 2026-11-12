package util

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
)

// The `process` section of the configuration (user, uid, gid, chroot,
// no_new_privs, app_namespaces) restricted the processes Semaphore starts for a
// task. Semaphore no longer applies any of it. A configuration that still
// carries the section would load without complaint, because the decoders ignore
// unknown keys and nothing reads the variables, and tasks would then run with
// the privileges of the Semaphore process itself. This file detects such a
// configuration so that startup can refuse it instead.

// removedProcessEnvVars lists the environment variables of the removed
// `process` section.
func removedProcessEnvVars() []string {
	return []string{
		"SEMAPHORE_PROCESS_USER",
		"SEMAPHORE_PROCESS_UID",
		"SEMAPHORE_PROCESS_GID",
		"SEMAPHORE_PROCESS_CHROOT",
		"SEMAPHORE_PROCESS_NO_NEW_PRIVS",
		"SEMAPHORE_PROCESS_APP_NS_USER",
		"SEMAPHORE_PROCESS_APP_NS_MOUNT",
		"SEMAPHORE_PROCESS_APP_NS_PID",
		"SEMAPHORE_PROCESS_APP_NS_IPC",
		"SEMAPHORE_PROCESS_APP_NS_UTS",
	}
}

// checkRemovedProcessSettings returns an error naming every removed `process`
// setting the configuration still switches on, in the config file at
// configPath (nil when no file was loaded) and in the environment. It returns
// nil when there is none: an empty `process` block, or one holding only empty,
// false and zero values, restricted nothing and is accepted.
func checkRemovedProcessSettings(inFile []string, configPath *string) error {
	var found []string

	if configPath != nil {
		for _, name := range inFile {
			found = append(found, fmt.Sprintf("%s (config file %s)", name, *configPath))
		}
	}

	for _, name := range removedProcessSettingsInEnv() {
		found = append(found, name+" (environment variable)")
	}

	if len(found) == 0 {
		return nil
	}

	return fmt.Errorf(`the "process" settings are no longer supported, but this configuration still sets:

  %s

Semaphore no longer switches the user, group or root directory, sets no_new_privs
or creates namespaces for the processes it starts. Ansible, Terraform/OpenTofu,
shell tasks and git now run with the privileges of the Semaphore process itself.

To keep tasks restricted, apply the restrictions to the Semaphore service (server
or runner) instead: for example User=, Group= and NoNewPrivileges= in the systemd
unit, or a non-root user for the container. Then remove the settings listed above
and start Semaphore again`, strings.Join(found, "\n  "))
}

// legacyProcess keeps the previous types so malformed legacy restrictions are
// rejected. Diagnostic values are kept separately to include unknown keys and
// their original spelling without ever including their values in an error.
type legacyProcess struct {
	User       string  `json:"user"`
	UID        *uint32 `json:"uid"`
	GID        *uint32 `json:"gid"`
	Chroot     string  `json:"chroot"`
	NoNewPrivs bool    `json:"no_new_privs"`
	Namespaces struct {
		User  bool `json:"user"`
		Mount bool `json:"mount"`
		PID   bool `json:"pid"`
		IPC   bool `json:"ipc"`
		UTS   bool `json:"uts"`
	} `json:"app_namespaces"`
	diagnostics map[string]any
}

func (p *legacyProcess) UnmarshalJSON(data []byte) error {
	type fields legacyProcess
	if err := json.Unmarshal(data, (*fields)(p)); err != nil {
		return err
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	p.diagnostics = mergeLegacyProcessObjects(p.diagnostics, object)
	return nil
}

// encoding/json merges repeated objects into struct fields. Merge diagnostics
// in the same order so an earlier active member is not hidden by a later object.
func mergeLegacyProcessObjects(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = make(map[string]any)
	}
	for key, value := range src {
		if nested, ok := value.(map[string]any); ok {
			if previous, ok := dst[key].(map[string]any); ok {
				value = mergeLegacyProcessObjects(previous, nested)
			}
		}
		dst[key] = value
	}
	return dst
}

func (p *legacyProcess) enabledSettings() ([]string, error) {
	names := enabledSettings("process", p.diagnostics)
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		seen[strings.ToLower(name)] = true
	}
	data, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var typed map[string]any
	if err := json.Unmarshal(data, &typed); err != nil {
		return nil, err
	}
	for _, name := range enabledSettings("process", typed) {
		if !seen[strings.ToLower(name)] {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names, nil
}

// enabledSettings walks a decoded JSON value and returns the dotted names of
// the values in it that switch something on: everything except null, an empty
// string, false, zero and an empty object or array.
func enabledSettings(name string, value any) (names []string) {
	switch v := value.(type) {
	case nil:
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			names = append(names, enabledSettings(name+"."+key, v[key])...)
		}
	case []any:
		if len(v) > 0 {
			names = append(names, name)
		}
	case string:
		if v != "" {
			names = append(names, name)
		}
	case bool:
		if v {
			names = append(names, name)
		}
	case float64:
		if v != 0 {
			names = append(names, name)
		}
	default:
		names = append(names, name)
	}
	return
}

// removedProcessSettingsInEnv lists the removed variables that are set to a
// active values. User and chroot are literal strings; numeric and boolean
// settings accept their zero representations.
func removedProcessSettingsInEnv() (names []string) {
	for _, name := range removedProcessEnvVars() {
		value := os.Getenv(name)
		if name == "SEMAPHORE_PROCESS_USER" || name == "SEMAPHORE_PROCESS_CHROOT" {
			if value != "" {
				names = append(names, name)
			}
			continue
		}
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && value != "0" && value != "false" {
			names = append(names, name)
		}
	}
	return
}
