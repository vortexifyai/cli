package pull

type Status string

const (
	StatusClean       Status = "clean"
	StatusServerAhead Status = "server_ahead"
	StatusLocalAhead  Status = "local_ahead"
	StatusConflict    Status = "conflict"
)

func DetermineStatus(local, base, server string) Status {
	if local == server {
		return StatusClean
	}
	if local == base {
		return StatusServerAhead
	}
	if server == base {
		return StatusLocalAhead
	}
	return StatusConflict
}

type ConflictError struct{ Resources []string }

func (e *ConflictError) Error() string {
	return "pull left conflicting resources unchanged: " + stringsJoin(e.Resources)
}
func stringsJoin(values []string) string {
	result := ""
	for i, v := range values {
		if i > 0 {
			result += ", "
		}
		result += v
	}
	return result
}
