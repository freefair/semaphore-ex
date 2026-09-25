package db_lib

import (
	"fmt"
	"github.com/semaphoreui/semaphore/util"
	"os"
	"path/filepath"
)

// InventoryResolverScript is shipped with the task bundle so local, Docker,
// and Kubernetes execution resolve inventories with the same Ansible context.
func InventoryResolverScript() string { return inventoryResolverScript }

func installInventoryResolver(directory string) (string, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	if util.Config != nil {
		if err := util.ChownDir(directory); err != nil {
			return "", fmt.Errorf("secure inventory resolver directory: %w", err)
		}
	}
	file, err := os.CreateTemp(directory, "inventory-resolver-*.py")
	if err != nil {
		return "", err
	}
	name := file.Name()
	_, writeErr := file.WriteString(inventoryResolverScript)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("write inventory resolver: %v %v", writeErr, closeErr)
	}
	if util.Config != nil {
		if err := util.ChownDir(name); err != nil {
			_ = os.Remove(name)
			return "", fmt.Errorf("secure inventory resolver: %w", err)
		}
	}
	return filepath.Abs(name)
}

const inventoryResolverScript = `from __future__ import annotations
import json
import os
import selectors
import signal
import subprocess
import sys
import tempfile


def emit(kind, **fields):
    print("SEMAPHORE_INVENTORY_RESULT " + json.dumps(dict(version=1, event=kind, **fields), separators=(",", ":")), flush=True)


def inventory_args(args):
    # Keep only options that affect inventory resolution. In particular, a
    # playbook's host limit must never turn into partial inventory membership.
    valued = {"-i", "--inventory", "--inventory-file", "-e", "--extra-vars", "--vault-id", "--vault-password-file", "--vault-pass-file"}
    result = []
    index = 0
    while index < len(args) - 1:
        value = args[index]
        flag = value.split("=", 1)[0]
        if flag in valued:
            result.append(value)
            if "=" not in value:
                index += 1
                if index >= len(args) - 1:
                    raise ValueError("missing option value")
                result.append(args[index])
        elif value.startswith(("-i", "-e")) and not value.startswith("--") and len(value) > 2:
            result.append(value)
        index += 1
    return result + ["--list", "--playbook-dir", os.path.dirname(os.path.abspath(args[-1]))]


def valid_name(name):
    return isinstance(name, str) and 0 < len(name.encode("utf-8")) <= 255 and not any(ord(c) < 32 or 127 <= ord(c) < 160 for c in name)


def membership(data):
    if not isinstance(data, dict):
        raise ValueError("invalid inventory")
    groups = {name: value for name, value in data.items() if name != "_meta"}
    hosts = {}
    def visit(name, parents):
        if name in parents or not valid_name(name):
            raise ValueError("invalid inventory group")
        group = groups.get(name, {})
        if not isinstance(group, dict):
            raise ValueError("invalid inventory group")
        ancestry = parents + [name]
        for host in group.get("hosts", []):
            if not valid_name(host):
                raise ValueError("invalid inventory host")
            # Match the existing structured Ansible task result identity.
            canonical = host.strip().lower().removesuffix(".")
            hosts.setdefault(canonical, set()).update(ancestry)
            if len(hosts) > 100000 or len(hosts[canonical]) > 256:
                raise ValueError("inventory limit exceeded")
        for child in group.get("children", []):
            visit(child, ancestry)
    for name in groups:
        visit(name, [])
    return hosts


def resolve(args, vault_inputs, private_dir):
    environment = dict(os.environ, ANSIBLE_FORCE_COLOR="False", ANSIBLE_INVENTORY_UNPARSED_FAILED="True", ANSIBLE_INVENTORY_ANY_UNPARSED_IS_FAILED="True")
    resolved_args = inventory_args(args)
    for index, value in enumerate(resolved_args):
        if value.startswith("--vault-id=") and value.endswith("@prompt"):
            name = value[len("--vault-id="):-len("@prompt")]
            password = vault_inputs["Vault password (%s):" % name]
            password_file = os.path.join(private_dir, "vault-%d" % index)
            with open(password_file, "x", opener=lambda path, flags: os.open(path, flags, 0o600)) as stream:
                stream.write(password)
            resolved_args[index] = "--vault-id=%s@%s" % (name, password_file)

    # The inventory command runs in a separate session so a task stop must
    # explicitly kill its process group. Block stop signals while it is born
    # and the wrapper's handlers are installed; otherwise SIGTERM can kill this
    # wrapper in that small window and leave the new session orphaned.
    stop_signals = {signal.SIGTERM, signal.SIGINT}
    original_mask = None
    if hasattr(signal, "pthread_sigmask"):
        original_mask = signal.pthread_sigmask(signal.SIG_BLOCK, stop_signals)

    def restore_child_mask():
        if original_mask is not None:
            signal.pthread_sigmask(signal.SIG_SETMASK, original_mask)

    process = None
    old_handlers = {}
    def stop(signum, frame):
        try:
            if process is not None:
                os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        raise SystemExit(128 + signum)
    try:
        process = subprocess.Popen(
            ["ansible-inventory"] + resolved_args,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            env=environment,
            start_new_session=True,
            preexec_fn=restore_child_mask if original_mask is not None else None,
        )
        for signum in stop_signals:
            old_handlers[signum] = signal.signal(signum, stop)
    finally:
        if original_mask is not None:
            signal.pthread_sigmask(signal.SIG_SETMASK, original_mask)
    try:
        # Bound raw, secret-bearing inventory output in memory. Neither stdout
        # nor diagnostics from the source are forwarded to task logs.
        raw = bytearray()
        with selectors.DefaultSelector() as selector:
            selector.register(process.stdout, selectors.EVENT_READ)
            while selector.get_map():
                for key, _ in selector.select():
                    chunk = os.read(key.fd, 65536)
                    if not chunk:
                        selector.unregister(key.fd)
                        continue
                    if len(raw) + len(chunk) > 64 * 1024 * 1024:
                        raise ValueError("inventory output limit exceeded")
                    raw.extend(chunk)
        if process.wait() != 0:
            raise ValueError("inventory resolution failed")
        return membership(json.loads(raw))
    finally:
        if process.poll() is None:
            os.killpg(process.pid, signal.SIGKILL)
        process.wait()
        process.stdout.close()
        for signum, handler in old_handlers.items():
            signal.signal(signum, handler)


def main():
    mode, args = sys.argv[1], sys.argv[2:]
    if mode not in ("run", "refresh") or not args:
        return 2
    emit("start")
    try:
        vault_inputs = json.loads(os.environ.pop("SEMAPHORE_INVENTORY_VAULT_INPUTS", "{}"))
        with tempfile.TemporaryDirectory(prefix="semaphore-inventory-") as private_dir:
            hosts = resolve(args, vault_inputs, private_dir)
        for host in sorted(hosts):
            emit("host", host=host, groups=sorted(hosts[host]))
        emit("complete", count=len(hosts))
    except Exception:
        emit("error")
        print("Inventory host resolution failed. Check the inventory source and template execution context.", flush=True)
        if mode == "refresh":
            return 1
    if mode == "refresh":
        print("Inventory hosts updated; no playbook was executed.", flush=True)
        return 0
    os.execvp("ansible-playbook", ["ansible-playbook"] + args)


if __name__ == "__main__":
    sys.exit(main())
`
