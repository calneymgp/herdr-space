package github

import (
	"net/url"
	"strings"
)

// RepositoryFromRemote returns an owner/repository pair only for a fixed
// github.com remote. It never returns or formats the supplied remote URL.
func RepositoryFromRemote(raw string) (string, bool) {
	if len(raw) == 0 || len(raw) > 4096 {
		return "", false
	}
	raw = strings.TrimSpace(raw)
	var host, repositoryPath string
	if strings.HasPrefix(raw, "git@") {
		hostAndPath := strings.TrimPrefix(raw, "git@")
		i := strings.IndexByte(hostAndPath, ':')
		if i < 0 {
			return "", false
		}
		host, repositoryPath = strings.ToLower(hostAndPath[:i]), hostAndPath[i+1:]
	} else {
		u, err := url.Parse(raw)
		if err != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" {
			return "", false
		}
		switch strings.ToLower(u.Scheme) {
		case "https":
			if u.Port() != "" && u.Port() != "443" {
				return "", false
			}
		case "ssh":
			if u.Port() != "" && u.Port() != "22" {
				return "", false
			}
		default:
			return "", false
		}
		host = strings.ToLower(u.Hostname())
		repositoryPath, err = url.PathUnescape(strings.TrimPrefix(u.EscapedPath(), "/"))
		if err != nil {
			return "", false
		}
	}
	if host != "github.com" {
		return "", false
	}
	repositoryPath = strings.TrimSuffix(repositoryPath, ".git")
	parts := strings.Split(repositoryPath, "/")
	if len(parts) != 2 || !validRepositoryPart(parts[0]) || !validRepositoryPart(parts[1]) {
		return "", false
	}
	return parts[0] + "/" + parts[1], true
}

func validRepositoryPart(part string) bool {
	if part == "" || part == "." || part == ".." || len(part) > 100 {
		return false
	}
	for _, r := range part {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

func validRepository(repository string) bool {
	parts := strings.Split(repository, "/")
	return len(parts) == 2 && validRepositoryPart(parts[0]) && validRepositoryPart(parts[1])
}
