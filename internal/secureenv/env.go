package secureenv

import "strings"

// ForUntrustedChild removes credential-shaped variables before Aegis starts
// repository-controlled tools or tests. Non-secret build configuration such as
// PATH, GOCACHE, GOPROXY, and GOFLAGS is preserved.
func ForUntrustedChild(environment []string) []string {
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || sensitiveName(name) {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func sensitiveName(name string) bool {
	name = strings.ToUpper(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	for _, exact := range []string{"DOCKER_CONFIG", "GIT_ASKPASS", "KUBECONFIG", "NETRC", "SSH_AUTH_SOCK"} {
		if name == exact {
			return true
		}
	}
	for _, fragment := range []string{"ACCESS_KEY", "AUTH", "COOKIE", "CREDENTIAL", "PASSWORD", "SECRET", "SESSION", "TOKEN"} {
		if strings.Contains(name, fragment) {
			return true
		}
	}
	return strings.HasSuffix(name, "_KEY")
}
