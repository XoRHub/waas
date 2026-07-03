package repository

import (
	"fmt"
	"time"
)

// Times are bound as RFC 3339 strings so the exact same SQL works on both
// PostgreSQL (implicit cast to timestamptz) and SQLite (stored as text).

func timeArg(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func timePtrArg(t *time.Time) any {
	if t == nil {
		return nil
	}
	return timeArg(*t)
}

// scanTime scans a timestamp regardless of how the driver surfaces it
// (time.Time from pgx, string/[]byte from SQLite).
type scanTime struct{ v *time.Time }

func (s scanTime) Scan(src any) error {
	switch x := src.(type) {
	case nil:
		*s.v = time.Time{}
		return nil
	case time.Time:
		*s.v = x.UTC()
		return nil
	case string:
		return s.parse(x)
	case []byte:
		return s.parse(string(x))
	default:
		return fmt.Errorf("scanning timestamp: unsupported type %T", src)
	}
}

func (s scanTime) parse(raw string) error {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, raw); err == nil {
			*s.v = t.UTC()
			return nil
		}
	}
	return fmt.Errorf("scanning timestamp: unrecognized format %q", raw)
}

// scanNullTime scans a nullable timestamp into **time.Time semantics.
type scanNullTime struct{ v **time.Time }

func (s scanNullTime) Scan(src any) error {
	if src == nil {
		*s.v = nil
		return nil
	}
	var t time.Time
	if err := (scanTime{v: &t}).Scan(src); err != nil {
		return err
	}
	*s.v = &t
	return nil
}
