package main

import (
	"strings"
	"testing"
)

const sampleYAML = `
bookings:
  - name: ทริปปีใหม่
    start_date: 2026-12-05
    end_date: 2026-12-06
    email: a@example.invalid
    people:
      - prefix: นาย
        first_name: สมชาย
        last_name: ตัวอย่าง
        phone: 0000000001        # unquoted: leading zero must survive
        birth_date: 1997-04-03
      - prefix: นาง
        first_name: สมหญิง
        last_name: ตัวอย่าง
        phone: "0000000002"
        birth_date: 1998-05-06
        id_card: 0000000000002
  - name: เดี่ยว
    start_date: 2026-12-19
    end_date: 2026-12-20
    services:
      shared_car: 0
      meal_set: 1
    people:
      - first_name: John
        last_name: Example
        phone: 0000000003
        birth_date: 1990-01-01
        passport: X0000000
`

func TestLoadBookings(t *testing.T) {
	bs, err := loadBookings(strings.NewReader(sampleYAML))
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 2 {
		t.Fatalf("want 2 bookings, got %d", len(bs))
	}
	a := bs[0]
	if a.Name != "ทริปปีใหม่" || len(a.People) != 2 || a.StartDate != "2026-12-05" || a.Email != "a@example.invalid" {
		t.Fatalf("bad booking A: %+v", a)
	}
	if a.People[0].Phone != "0000000001" || a.People[1].IDCard != "0000000000002" {
		t.Fatalf("leading zeros lost: %+v", a.People)
	}
}

func TestDefaultServicesScaleWithPeople(t *testing.T) {
	bs, err := loadBookings(strings.NewReader(sampleYAML))
	if err != nil {
		t.Fatal(err)
	}
	// 2 people, no services given: shared_car=2, own_tent_space=2, own_tent_count=1
	if !strings.Contains(bs[0].Service1, `{"id":3,"qty":2}`) {
		t.Fatalf("service1 = %s", bs[0].Service1)
	}
	if bs[0].Service2 != `[{"id":17,"qty":0},{"id":25,"qty":2},{"id":26,"qty":1}]` {
		t.Fatalf("service2 = %s", bs[0].Service2)
	}
}

func TestExplicitServicesOverrideDefaults(t *testing.T) {
	bs, err := loadBookings(strings.NewReader(sampleYAML))
	if err != nil {
		t.Fatal(err)
	}
	s1 := bs[1].Service1
	if !strings.Contains(s1, `{"id":3,"qty":0}`) || !strings.Contains(s1, `{"id":35,"qty":1}`) {
		t.Fatalf("service1 = %s", s1)
	}
	// every known service1 id is sent, like the browser does
	for _, id := range []string{`"id":9,`, `"id":14,`} {
		if !strings.Contains(s1, id) {
			t.Fatalf("service1 missing %s: %s", id, s1)
		}
	}
}

func TestLoadBookingsErrors(t *testing.T) {
	cases := map[string]string{
		"missing start_date": "bookings:\n- name: x\n  end_date: 2026-12-06\n  people:\n  - {first_name: a, last_name: b, phone: '0800000000', birth_date: 1990-01-01}\n",
		"no people":          "bookings:\n- name: x\n  start_date: 2026-12-05\n  end_date: 2026-12-06\n",
		"unknown service":    "bookings:\n- name: x\n  start_date: 2026-12-05\n  end_date: 2026-12-06\n  services: {spaceship: 1}\n  people:\n  - {first_name: a, last_name: b, phone: '0800000000', birth_date: 1990-01-01}\n",
		"typo field":         "bookings:\n- name: x\n  start_date: 2026-12-05\n  end_date: 2026-12-06\n  people:\n  - {first_name: a, last_name: b, phone: '0800000000', birthdate: 1990-01-01}\n",
		"phone too short":    "bookings:\n- name: x\n  start_date: 2026-12-05\n  end_date: 2026-12-06\n  people:\n  - {first_name: a, last_name: b, phone: 812345678, birth_date: 1990-01-01}\n",
		"duplicate name":     "bookings:\n- name: x\n  start_date: 2026-12-05\n  end_date: 2026-12-06\n  people:\n  - {first_name: a, last_name: b, phone: '0800000000', birth_date: 1990-01-01}\n- name: x\n  start_date: 2026-12-05\n  end_date: 2026-12-06\n  people:\n  - {first_name: a, last_name: b, phone: '0800000000', birth_date: 1990-01-01}\n",
	}
	one := "  people:\n  - {first_name: a, last_name: b, phone: '0800000000', birth_date: 1990-01-01}\n"
	cases["3 people"] = "bookings:\n- name: x\n  start_date: 2026-12-05\n  end_date: 2026-12-06\n" + one +
		"  - {first_name: c, last_name: d, phone: '0800000001', birth_date: 1990-01-01}\n" +
		"  - {first_name: e, last_name: f, phone: '0800000002', birth_date: 1990-01-01}\n"
	cases["2 nights"] = "bookings:\n- name: x\n  start_date: 2026-12-05\n  end_date: 2026-12-07\n" + one
	cases["wednesday"] = "bookings:\n- name: x\n  start_date: 2026-12-16\n  end_date: 2026-12-17\n" + one
	cases["before season"] = "bookings:\n- name: x\n  start_date: 2026-11-14\n  end_date: 2026-11-15\n" + one
	cases["after season"] = "bookings:\n- name: x\n  start_date: 2027-02-13\n  end_date: 2027-02-14\n" + one
	for name, y := range cases {
		if _, err := loadBookings(strings.NewReader(y)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestFormFieldsMatchSite(t *testing.T) {
	bs, err := loadBookings(strings.NewReader(sampleYAML))
	if err != nil {
		t.Fatal(err)
	}
	fields, err := bs[0].formFields()
	if err != nil {
		t.Fatal(err)
	}
	get := func(name string) []string {
		var v []string
		for _, f := range fields {
			if f[0] == name {
				v = append(v, f[1])
			}
		}
		return v
	}
	if q := get("qty_people"); len(q) != 1 || q[0] != "2" {
		t.Fatalf("qty_people = %v", q)
	}
	if fn := get("first_name"); len(fn) != 2 || fn[1] != "สมหญิง" {
		t.Fatalf("first_name = %v", fn)
	}
	if f := get("id_card_file2"); len(f) != 1 || f[0] != "null" {
		t.Fatalf("id_card_file2 = %v", f)
	}
	if bd := get("birth_date"); bd[0] != "Thu Apr 03 1997 00:00:00 GMT+0700 (Indochina Time)" {
		t.Fatalf("birth_date = %v", bd)
	}
	enc := get("enc")
	if len(enc) != 1 {
		t.Fatalf("enc = %v", enc)
	}
	plain, err := cryptoJSDecrypt(enc[0], encPassphrase)
	if err != nil || !strings.Contains(string(plain), `"first_name":"สมชาย"`) {
		t.Fatalf("enc must describe first person, got %s (%v)", plain, err)
	}
	if len(get("code")) != 0 {
		t.Fatal("code must be omitted when empty")
	}
}
