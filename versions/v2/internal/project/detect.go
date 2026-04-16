package project

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func Detect(directory string) string {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return "default"
	}
	if strings.HasPrefix(directory, "-") {
		directory = "./" + directory
	}

	if name := gitRemoteName(directory); name != "" {
		return normalize(name)
	}
	if name := gitRootName(directory); name != "" {
		return normalize(name)
	}
	if name := filepath.Base(directory); name != "" && name != "." {
		return normalize(name)
	}
	return "default"
}

func gitRemoteName(directory string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	out, err := exec.CommandContext(ctx, "git", "-C", directory, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return repoNameFromRemote(strings.TrimSpace(string(out)))
}

func gitRootName(directory string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	out, err := exec.CommandContext(ctx, "git", "-C", directory, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return ""
	}
	return filepath.Base(root)
}

func repoNameFromRemote(remote string) string {
	remote = strings.TrimSuffix(strings.TrimSpace(remote), ".git")
	if remote == "" {
		return ""
	}
	parts := strings.FieldsFunc(remote, func(r rune) bool {
		return r == '/' || r == ':'
	})
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[len(parts)-1])
}

func normalize(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.Join(strings.Fields(name), "-")
	if name == "" {
		return "default"
	}
	return name
}
