// Command releaseline reports which upstream Semaphore UI release a branch is
// based on and which Semaphore EX release tag comes next.
//
// The fork merges only final upstream release tags (vX.Y.Z). Its own releases
// are vX.Y.Z-ex.N[.M[.P]]: X.Y.Z is the newest final upstream release merged
// into the branch, and the EX number continues across upstream lines.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

type version struct{ major, minor, patch int }

func (v version) less(o version) bool {
	if v.major != o.major {
		return v.major < o.major
	}
	if v.minor != o.minor {
		return v.minor < o.minor
	}
	return v.patch < o.patch
}

func (v version) String() string { return fmt.Sprintf("v%d.%d.%d", v.major, v.minor, v.patch) }

// parseFinal accepts only final upstream release tags; pre-releases and fork
// tags never define the base line.
func parseFinal(tag string) (version, bool) {
	m := regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`).FindStringSubmatch(tag)
	if m == nil {
		return version{}, false
	}
	var parts [3]int
	for i := range parts {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return version{}, false
		}
		parts[i] = n
	}
	return version{parts[0], parts[1], parts[2]}, true
}

// parseEX returns the numeric EX components of a final fork release tag.
// Release candidates are excluded so they never advance the sequence.
func parseEX(tag string) ([]int, bool) {
	m := regexp.MustCompile(`^v\d+\.\d+\.\d+-ex\.(\d+)(?:\.(\d+))?(?:\.(\d+))?$`).FindStringSubmatch(tag)
	if m == nil {
		return nil, false
	}
	var ex []int
	for _, s := range m[1:] {
		if s == "" {
			break
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, false
		}
		ex = append(ex, n)
	}
	return ex, true
}

// compareEX orders EX tuples numerically; a tuple sorts after its own prefix.
func compareEX(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return len(a) - len(b)
}

func latestFinal(tags []string) (string, bool) {
	var best version
	found := false
	for _, tag := range tags {
		v, ok := parseFinal(tag)
		if ok && (!found || best.less(v)) {
			best, found = v, true
		}
	}
	if !found {
		return "", false
	}
	return best.String(), true
}

// nextEX increments the last component of the highest existing EX number and
// places it on the given upstream base line.
func nextEX(base string, tags []string) (string, error) {
	if _, ok := parseFinal(base); !ok {
		return "", fmt.Errorf("%s is not a final upstream release tag", base)
	}
	var highest []int
	for _, tag := range tags {
		if ex, ok := parseEX(tag); ok && compareEX(ex, highest) > 0 {
			highest = ex
		}
	}
	if highest == nil {
		return base + "-ex.1", nil
	}
	next := append([]int(nil), highest...)
	next[len(next)-1]++
	parts := make([]string, len(next))
	for i, n := range next {
		parts[i] = strconv.Itoa(n)
	}
	return base + "-ex." + strings.Join(parts, "."), nil
}

type gitRepo struct{ dir string }

func (r gitRepo) output(args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", r.dir}, args...)...).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (r gitRepo) tags() ([]string, error) {
	out, err := r.output("tag", "--list")
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

func (r gitRepo) commit(ref string) (string, error) {
	return r.output("rev-parse", "--verify", ref+"^{commit}")
}

func (r gitRepo) isAncestor(ancestor, head string) (bool, error) {
	err := exec.Command("git", "-C", r.dir, "merge-base", "--is-ancestor", ancestor, head).Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w", ancestor, head, err)
}

// mergedBase finds the newest final upstream release contained in head.
func mergedBase(r gitRepo, head string, tags []string) (string, error) {
	var best version
	found := false
	for _, tag := range tags {
		v, ok := parseFinal(tag)
		if !ok || (found && !best.less(v)) {
			continue
		}
		in, err := r.isAncestor(tag, head)
		if err != nil {
			return "", err
		}
		if in {
			best, found = v, true
		}
	}
	if !found {
		return "", fmt.Errorf("%s contains no final upstream release tag", head)
	}
	return best.String(), nil
}

func run1(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("releaseline", flag.ContinueOnError)
	flags.SetOutput(stderr)
	mode := flags.String("mode", "latest", "latest | base | next")
	repoDir := flags.String("repo", ".", "repository directory")
	head := flags.String("head", "HEAD", "branch or commit to assess")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	r := gitRepo{dir: *repoDir}
	tags, err := r.tags()
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	switch *mode {
	case "latest":
		tag, ok := latestFinal(tags)
		if !ok {
			fmt.Fprintln(stderr, "error: no final upstream release tag; fetch upstream tags first")
			return 1
		}
		sha, err := r.commit(tag)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		merged, err := r.isAncestor(tag, *head)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		fmt.Fprintf(stdout, "tag: %s\ncommit: %s\nmerged: %t\n", tag, sha, merged)
	case "base", "next":
		base, err := mergedBase(r, *head, tags)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		if *mode == "base" {
			fmt.Fprintln(stdout, base)
			return 0
		}
		next, err := nextEX(base, tags)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		fmt.Fprintln(stdout, next)
	default:
		fmt.Fprintf(stderr, "error: unknown mode %q\n", *mode)
		return 2
	}
	return 0
}

func main() { os.Exit(run1(os.Args[1:], os.Stdout, os.Stderr)) }
