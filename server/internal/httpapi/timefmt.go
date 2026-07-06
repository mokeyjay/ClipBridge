package httpapi

import "time"

// rfc3339 renders Unix seconds as a UTC RFC 3339 string for API responses.
func rfc3339(sec int64) string {
	return time.Unix(sec, 0).UTC().Format(time.RFC3339)
}

// rfc3339Ptr renders a nullable Unix-seconds timestamp; nil becomes "".
func rfc3339Ptr(sec *int64) string {
	if sec == nil {
		return ""
	}
	return rfc3339(*sec)
}
