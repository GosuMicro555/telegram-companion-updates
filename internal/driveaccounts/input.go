package driveaccounts

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

const MaxLinks = 100

var ErrInput = errors.New("input_invalid")
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

// Reference is kept in memory only, never in progress DTOs or logs.
type Reference struct {
	ID          string
	ResourceKey string
	Folder      bool
}

func ParseLinks(raw string) ([]Reference, error) {
	if len(raw) > 256*1024 {
		return nil, ErrInput
	}
	result := make([]Reference, 0)
	seen := map[string]bool{}
	count := 0
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		count++
		if count > MaxLinks {
			return nil, ErrInput
		}
		ref, err := parseReference(line)
		if err != nil {
			return nil, err
		}
		key := ref.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, ref)
	}
	if len(result) == 0 {
		return nil, ErrInput
	}
	return result, nil
}
func parseReference(raw string) (Reference, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "drive.google.com" || u.User != nil || u.Fragment != "" || u.RawPath != "" {
		return Reference{}, ErrInput
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return Reference{}, ErrInput
	}
	for key, values := range q {
		if len(values) != 1 {
			return Reference{}, ErrInput
		}
		switch key {
		case "id", "resourcekey", "usp":
		case "export":
			if values[0] != "download" {
				return Reference{}, ErrInput
			}
		default:
			return Reference{}, ErrInput
		}
	}
	r := Reference{ResourceKey: q.Get("resourcekey")}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case u.Path == "/uc" || u.Path == "/open":
		r.ID = q.Get("id")
	case len(parts) >= 3 && len(parts) <= 4 && parts[0] == "file" && parts[1] == "d":
		if len(parts) == 4 && parts[3] != "view" && parts[3] != "edit" {
			return r, ErrInput
		}
		r.ID = parts[2]
	case len(parts) == 3 && parts[0] == "drive" && parts[1] == "folders":
		r.ID = parts[2]
		r.Folder = true
	default:
		return r, ErrInput
	}
	if !idPattern.MatchString(r.ID) || (r.ResourceKey != "" && !idPattern.MatchString(r.ResourceKey)) {
		return Reference{}, ErrInput
	}
	if q.Get("id") != "" && q.Get("id") != r.ID {
		return Reference{}, ErrInput
	}
	return r, nil
}
