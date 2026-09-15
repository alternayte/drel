package drel

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDate_RoundTripsThroughTheDriverForms(t *testing.T) {
	d := NewDate(2026, time.January, 2)
	if got := d.String(); got != "2026-01-02" {
		t.Fatalf("String() = %q", got)
	}

	v, err := d.Value()
	if err != nil {
		t.Fatal(err)
	}
	if v != "2026-01-02" {
		t.Fatalf("Value() = %v", v)
	}

	// PostgreSQL decodes a date column to time.Time; SQLite returns text.
	for _, src := range []any{
		time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		"2026-01-02",
		[]byte("2026-01-02"),
		"2026-01-02 00:00:00+00", // SQLite may hold a full timestamp as text
	} {
		var got Date
		if err := got.Scan(src); err != nil {
			t.Fatalf("Scan(%T): %v", src, err)
		}
		if !got.Equal(d) {
			t.Fatalf("Scan(%T) = %s, want %s", src, got, d)
		}
	}

	var zero Date
	if err := zero.Scan(nil); err != nil || !zero.IsZero() {
		t.Fatalf("Scan(nil) = %s, %v", zero, err)
	}
}

func TestDate_RejectsADayThatDoesNotExist(t *testing.T) {
	if _, err := ParseDate("2026-02-30"); err == nil {
		t.Fatal("2026-02-30 should not parse")
	}
	var d Date
	if err := d.Scan("not a date"); err == nil {
		t.Fatal("Scan should reject text that is not a date")
	}
}

func TestDate_ComparesAndOrders(t *testing.T) {
	a := NewDate(2026, time.January, 2)
	b := NewDate(2026, time.March, 4)

	if !a.Before(b) || !b.After(a) || a.Equal(b) {
		t.Fatal("ordering is wrong")
	}
	if a != NewDate(2026, time.January, 2) {
		t.Fatal("Date must be comparable with ==, so change tracking can diff it")
	}
	if got := a.AddDays(30); got != NewDate(2026, time.February, 1) {
		t.Fatalf("AddDays(30) = %s", got)
	}
	if got := DateOf(time.Date(2026, 5, 6, 23, 59, 0, 0, time.UTC)); got != NewDate(2026, time.May, 6) {
		t.Fatalf("DateOf = %s", got)
	}
}

func TestDate_JSONIsTheISOForm(t *testing.T) {
	type payload struct {
		Day Date `json:"day"`
	}
	b, err := json.Marshal(payload{Day: NewDate(2026, time.January, 2)})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"day":"2026-01-02"}` {
		t.Fatalf("Marshal = %s", b)
	}

	var got payload
	if err := json.Unmarshal([]byte(`{"day":"2026-03-04"}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.Day != NewDate(2026, time.March, 4) {
		t.Fatalf("Unmarshal = %s", got.Day)
	}
	if err := json.Unmarshal([]byte(`{"day":"nope"}`), &got); err == nil {
		t.Fatal("Unmarshal should reject text that is not a date")
	}
}
