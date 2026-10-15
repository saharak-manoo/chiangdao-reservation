package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// service is one optional add-on the form sends as {"id":N,"qty":N}.
// IDs come from GET service/prepayment (list 1) and POST service/payment-at-sanc (list 2).
type service struct {
	name string
	list int // 1 = service1 (prepaid), 2 = service2 (paid at sanctuary)
	id   int
	// defaultQty is used when the booking doesn't set the service.
	defaultQty func(people int) int
}

func zero(int) int        { return 0 }
func one(int) int         { return 1 }
func perPerson(n int) int { return n }

// Order matters: it is the order the browser sends them in.
var serviceCatalog = []service{
	{"shared_car", 1, 3, perPerson},      // แชร์ค่ารถ 600/คน
	{"tent_rent", 1, 9, zero},            // เต็นท์ (เช่าของเขต) 300/เต็นท์
	{"gas_stove", 1, 10, zero},           // เตาแก๊ส 100/เตา
	{"sleeping_pad", 1, 11, zero},        // แผ่นรองนอน 50/แผ่น
	{"pot", 1, 12, zero},                 // หม้อสนาม 80/ใบ
	{"gas_can", 1, 13, zero},             // แก๊สกระป๋อง 60/กระป๋อง
	{"sleeping_bag", 1, 14, zero},        // ถุงนอน 100/ถุง
	{"meal_set", 1, 35, zero},            // ชุดอาหาร 4 มื้อ 950/คน
	{"porter", 2, 17, zero},              // ลูกหาบ
	{"own_tent_space", 2, 25, perPerson}, // พื้นที่กางเต็นท์ (นำมาเอง) 30/คน
	{"own_tent_count", 2, 26, one},       // จำนวนเต็นท์ที่นำมาเอง
}

type Person struct {
	Prefix    string `yaml:"prefix"`
	FirstName string `yaml:"first_name"`
	LastName  string `yaml:"last_name"`
	Phone     string `yaml:"phone"`
	BirthDate string `yaml:"birth_date"`
	IDCard    string `yaml:"id_card"`
	Passport  string `yaml:"passport"`
}

// Booking is one reservation. The first person is the main booker.
type Booking struct {
	Name        string         `yaml:"name"`
	StartDate   string         `yaml:"start_date"`
	EndDate     string         `yaml:"end_date"`
	Email       string         `yaml:"email"`
	Code        string         `yaml:"code"`
	Bank        string         `yaml:"bank"`
	BankAccNo   string         `yaml:"bank_acc_no"`
	BankAccName string         `yaml:"bank_acc_name"`
	Services    map[string]int `yaml:"services"`
	People      []Person       `yaml:"people"`

	// Built from Services by loadBookings.
	Service1 string `yaml:"-"`
	Service2 string `yaml:"-"`
}

var (
	dateRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	phoneRe = regexp.MustCompile(`^0\d{9}$`)
)

func loadBookings(r io.Reader) ([]Booking, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true) // typo in a field name = error, not silently empty
	var file struct {
		Bookings []Booking `yaml:"bookings"`
	}
	if err := dec.Decode(&file); err != nil {
		return nil, err
	}
	if len(file.Bookings) == 0 {
		return nil, errors.New("no bookings in file")
	}

	seen := map[string]bool{}
	for i := range file.Bookings {
		b := &file.Bookings[i]
		label := b.Name
		if label == "" {
			label = fmt.Sprintf("booking #%d", i+1)
			b.Name = strconv.Itoa(i + 1)
		}
		if seen[b.Name] {
			return nil, fmt.Errorf("%s: duplicate name", label)
		}
		seen[b.Name] = true
		if err := b.validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		if err := b.buildServices(); err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
	}
	return file.Bookings, nil
}

func (b *Booking) validate() error {
	if !dateRe.MatchString(b.StartDate) || !dateRe.MatchString(b.EndDate) {
		return fmt.Errorf("start_date and end_date are required as YYYY-MM-DD (got %q, %q)", b.StartDate, b.EndDate)
	}
	if b.EndDate <= b.StartDate {
		return fmt.Errorf("end_date %s must be after start_date %s", b.EndDate, b.StartDate)
	}
	if len(b.People) == 0 {
		return errors.New("people is empty")
	}
	for i, p := range b.People {
		who := fmt.Sprintf("person %d (%s)", i+1, p.FirstName)
		switch {
		case p.FirstName == "" || p.LastName == "":
			return fmt.Errorf("%s: first_name and last_name are required", who)
		case !phoneRe.MatchString(p.Phone):
			return fmt.Errorf("%s: phone %q must be 10 digits starting with 0", who, p.Phone)
		case !dateRe.MatchString(p.BirthDate):
			return fmt.Errorf("%s: birth_date %q must be YYYY-MM-DD", who, p.BirthDate)
		}
	}
	return nil
}

func (b *Booking) buildServices() error {
	known := map[string]bool{}
	for _, s := range serviceCatalog {
		known[s.name] = true
	}
	for name, qty := range b.Services {
		if !known[name] {
			return fmt.Errorf("unknown service %q (known: %s)", name, serviceNames())
		}
		if qty < 0 {
			return fmt.Errorf("service %s: qty must be >= 0", name)
		}
	}

	type item struct {
		ID  int `json:"id"`
		Qty int `json:"qty"`
	}
	lists := map[int][]item{1: {}, 2: {}}
	for _, s := range serviceCatalog {
		qty, ok := b.Services[s.name]
		if !ok {
			qty = s.defaultQty(len(b.People))
		}
		lists[s.list] = append(lists[s.list], item{s.id, qty})
	}
	s1, _ := json.Marshal(lists[1])
	s2, _ := json.Marshal(lists[2])
	b.Service1, b.Service2 = string(s1), string(s2)
	return nil
}

func serviceNames() string {
	names := make([]string, len(serviceCatalog))
	for i, s := range serviceCatalog {
		names[i] = s.name
	}
	return strings.Join(names, ", ")
}

// formFields builds the multipart fields in the same order as the site's JS
// (chunk 9495: reservation form builder). Pairs, because person fields repeat.
func (b Booking) formFields() ([][2]string, error) {
	f := [][2]string{
		{"start_date", b.StartDate},
		{"end_date", b.EndDate},
		{"qty_people", strconv.Itoa(len(b.People))},
	}
	for i, p := range b.People {
		bd, err := jsDateString(p.BirthDate)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", p.FirstName, p.LastName, err)
		}
		f = append(f,
			[2]string{"prefix", p.Prefix},
			[2]string{"first_name", p.FirstName},
			[2]string{"last_name", p.LastName},
			[2]string{"phone", p.Phone},
			[2]string{"birth_date", bd},
			[2]string{"id_card_number", p.IDCard},
			[2]string{"passport", p.Passport},
			[2]string{"id_card_file" + strconv.Itoa(i+1), "null"},
		)
	}
	if b.Code != "" {
		f = append(f, [2]string{"code", b.Code})
	}
	f = append(f,
		[2]string{"email", b.Email},
		[2]string{"bank", b.Bank},
		[2]string{"bank_acc_no", b.BankAccNo},
		[2]string{"bank_acc_name", b.BankAccName},
	)
	first := b.People[0]
	enc, err := buildEnc(first.FirstName, first.IDCard, first.Phone)
	if err != nil {
		return nil, err
	}
	f = append(f,
		[2]string{"enc", enc},
		[2]string{"service1", b.Service1},
		[2]string{"service2", b.Service2},
	)
	return f, nil
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
