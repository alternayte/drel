package drel

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// DateLayout is the wire form of a Date: the ISO 8601 calendar date.
const DateLayout = "2006-01-02"

// Date is a calendar date with no time and no zone, stored in a SQL `date`
// column. A `time.Time` field maps to a timestamp, which carries a time of day
// and a zone that a calendar date does not have: two clients in different zones
// disagree about which day a timestamp falls on.
//
// Date is a value object: it implements sql.Scanner and driver.Valuer, so
// codegen maps it without special handling in your model, and it is comparable,
// so change tracking diffs it with no extra method.
//
// The zero Date is the zero year, which a database stores as 0001-01-01. Use a
// *Date for a column that may hold NULL.
type Date struct {
	year  int
	month int
	day   int
}

// NewDate returns the date for a year, month and day. It does not validate the
// combination: use ParseDate for input that may be wrong, or DateOf to take the
// date part of a time.Time.
func NewDate(year int, month time.Month, day int) Date {
	return Date{year: year, month: int(month), day: day}
}

// DateOf returns the calendar date of t in t's own location.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{year: y, month: int(m), day: d}
}

// Today returns the current date in the local zone.
func Today() Date { return DateOf(time.Now()) }

// ParseDate reads a date in the 2006-01-02 form, rejecting a day that does not
// exist.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		return Date{}, fmt.Errorf("drel: parse date %q: %w", s, err)
	}
	return DateOf(t), nil
}

// Year, Month and Day return the parts of the date.
func (d Date) Year() int         { return d.year }
func (d Date) Month() time.Month { return time.Month(d.month) }
func (d Date) Day() int          { return d.day }

// Time returns midnight of the date in loc. A nil loc means UTC.
func (d Date) Time(loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	return time.Date(d.year, time.Month(d.month), d.day, 0, 0, 0, 0, loc)
}

// String returns the date in the 2006-01-02 form.
func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.year, d.month, d.day)
}

// IsZero reports whether d is the zero Date. It is deliberately not the
// zero<->NULL bridge that a value object opts into with an IsZero method on a
// pointer receiver: a Date column stays NOT NULL unless the field is a pointer.
func (d Date) IsZero() bool { return d == Date{} }

// Equal reports whether two dates are the same day.
func (d Date) Equal(o Date) bool { return d == o }

// Before and After order two dates.
func (d Date) Before(o Date) bool {
	if d.year != o.year {
		return d.year < o.year
	}
	if d.month != o.month {
		return d.month < o.month
	}
	return d.day < o.day
}

func (d Date) After(o Date) bool { return o.Before(d) }

// AddDays returns the date n days later, normalising the result.
func (d Date) AddDays(n int) Date { return DateOf(d.Time(time.UTC).AddDate(0, 0, n)) }

// Value writes the date as its ISO form. PostgreSQL accepts it for a `date`
// column, and SQLite stores the text.
func (d Date) Value() (driver.Value, error) { return d.String(), nil }

// Scan reads a date from a driver value. PostgreSQL decodes a `date` column to
// time.Time; SQLite returns the stored text.
func (d *Date) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*d = Date{}
		return nil
	case time.Time:
		*d = DateOf(v)
		return nil
	case string:
		return d.scanText(v)
	case []byte:
		return d.scanText(string(v))
	default:
		return fmt.Errorf("drel: Date.Scan: cannot read %T", src)
	}
}

func (d *Date) scanText(s string) error {
	if s == "" {
		*d = Date{}
		return nil
	}
	// SQLite may hold a full timestamp in a column written as text.
	if len(s) > len(DateLayout) {
		s = s[:len(DateLayout)]
	}
	parsed, err := ParseDate(s)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// MarshalJSON writes the date as a JSON string, so an API answer carries
// "2026-01-02" rather than a timestamp.
func (d Date) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// UnmarshalJSON reads a date from a JSON string. A null leaves the zero Date.
func (d *Date) UnmarshalJSON(data []byte) error {
	var s *string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	if s == nil {
		*d = Date{}
		return nil
	}
	parsed, err := ParseDate(*s)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}
