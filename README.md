# chiangdao-reservation

จองเขตรักษาพันธุ์สัตว์ป่าเชียงดาว (wildlifesanctuaryfca16.com) อัตโนมัติจากไฟล์ YAML

## ใช้งาน

```bash
go build -o chiangdao-reservation .
cp bookings.example.yaml bookings.yaml   # แก้ข้อมูลจริง (bookings.yaml อยู่ใน .gitignore)

./chiangdao-reservation                                     # dry-run: แสดงสิ่งที่จะส่ง ไม่ยิงจริง
./chiangdao-reservation -submit                             # จองจริงทันที
./chiangdao-reservation -submit -at "2026-10-17 09:00:00"   # รอถึงเวลา (เวลาไทย) แล้วค่อยยิง
./chiangdao-reservation -submit -name ทริปปีใหม่             # จองเฉพาะการจองชื่อนี้
./chiangdao-reservation -check                              # ดูสถานะการจองทั้งหมด (ไม่จอง)
```

| flag | default | ความหมาย |
|---|---|---|
| `-file` | `bookings.yaml` | ไฟล์ข้อมูล |
| `-submit` | `false` | ไม่ใส่ = dry-run เท่านั้น |
| `-check` | `false` | ดูสถานะการจองอย่างเดียว |
| `-at` | ว่าง | เวลาเริ่มยิง `YYYY-MM-DD HH:MM:SS` (Asia/Bangkok) |
| `-name` | ว่าง | จองเฉพาะการจองชื่อนี้ |
| `-retries` | `5` | จำนวนครั้งสูงสุดต่อการจอง (retry เฉพาะ network error / HTTP 5xx) |
| `-retry-delay` | `2s` | เว้นระหว่าง retry |

## รูปแบบไฟล์ YAML

ดูตัวอย่างเต็มใน [bookings.example.yaml](bookings.example.yaml)

```yaml
bookings:
  - name: ทริปปีใหม่          # ชื่อเรียกเอง ห้ามซ้ำ
    start_date: 2026-01-31
    end_date: 2026-02-01
    email: me@example.com     # ไม่บังคับ
    services:                 # ไม่บังคับ ใส่เฉพาะตัวที่ต่างจาก default
      meal_set: 2
    people:                   # คนแรก = ผู้จองหลัก
      - prefix: นาย
        first_name: สมชาย
        last_name: ใจดี
        phone: 0812345678
        birth_date: 1997-04-03
        id_card: 1234567890123  # หรือ passport: X1234567
```

- 1 การจอง = 1 ใบจอง, `qty_people` = จำนวนคนใน `people`
- ไม่ต้องใส่ `"..."` ครอบเบอร์/เลขบัตร — เลข 0 ข้างหน้าไม่หาย
- พิมพ์ชื่อ field ผิด → error บอกบรรทัด (ไม่เงียบ)
- ตรวจ: วันที่เป็น `YYYY-MM-DD`, `end_date` > `start_date`, เบอร์ 10 หลักขึ้นต้น 0, มีอย่างน้อย 1 คน, ชื่อ service ต้องรู้จัก
- `birth_date` ถูกแปลงเป็น `Thu Apr 03 1997 00:00:00 GMT+0700 (Indochina Time)` เหมือน browser

### services

ส่งทุกตัวไปเสมอ (เหมือน browser) ตัวที่ไม่ใส่ใช้ค่า default

| ชื่อ | list / id | default | ความหมาย |
|---|---|---|---|
| `shared_car` | 1 / 3 | จำนวนคน | แชร์ค่ารถ 600/คน |
| `tent_rent` | 1 / 9 | 0 | เต็นท์ (เช่าของเขต) 300/เต็นท์ |
| `gas_stove` | 1 / 10 | 0 | เตาแก๊ส 100/เตา |
| `sleeping_pad` | 1 / 11 | 0 | แผ่นรองนอน 50/แผ่น |
| `pot` | 1 / 12 | 0 | หม้อสนาม 80/ใบ |
| `gas_can` | 1 / 13 | 0 | แก๊สกระป๋อง 60/กระป๋อง |
| `sleeping_bag` | 1 / 14 | 0 | ถุงนอน 100/ถุง |
| `meal_set` | 1 / 35 | 0 | ชุดอาหาร 4 มื้อ 950/คน |
| `porter` | 2 / 17 | 0 | ลูกหาบ (จ่ายที่เขตฯ) |
| `own_tent_space` | 2 / 25 | จำนวนคน | พื้นที่กางเต็นท์ที่นำมาเอง 30/คน (จ่ายที่เขตฯ) |
| `own_tent_count` | 2 / 26 | 1 | จำนวนเต็นท์ที่นำมาเอง |

id มาจาก `GET /api/chiangdao/service/prepayment` (list 1) และ `POST /api/chiangdao/service/payment-at-sanc` (list 2) ณ ต.ค. 2026.
curl รอบ ม.ค. 2026 มี id `4` และ `34` ซึ่งไม่อยู่ในรายการปัจจุบันแล้ว (`35` มาแทน) — ถ้าเว็บเปลี่ยนอีก แก้ `serviceCatalog` ใน `booking.go`.
default ของ `shared_car`/`own_tent_space` = จำนวนคน เป็นการเดา (curl ตัวอย่างมี 1 คน qty 1) — ยังไม่ยืนยันกับการจองหลายคนจริง.

## ลำดับการทำงาน (ต่อ 1 การจอง)

1. `GET /api/chiangdao/reservation/check-reserve?phone=&id_card_number=` (เบอร์ + บัตร/passport ของคนแรก) → ถ้ามีใบจองวัน `start_date` เดียวกันที่ไม่ใช่ `CANCEL` อยู่แล้ว ข้าม (กันจองซ้ำตอนรันใหม่)
2. `GET /api/chiangdao/reservation/check-qty-people?start_date=` → ถ้าที่ว่าง (`qty_reserve`) < จำนวนคน ข้าม
3. `POST /api/chiangdao/reservation/form` (multipart, ลำดับ field เหมือน JS ของเว็บ, สร้าง `enc` ใหม่ทุกครั้ง)
4. `status == "success"` → ยืนยันด้วย `check-reserve` อีกรอบ (สูงสุด 3 ครั้ง ห่าง 2 วินาที) แล้ว log `BOOKED ✔` + เลขใบจอง/สถานะ/ยอด/หมดอายุ
   HTTP 4xx/ธุรกิจ error → หยุดการจองนั้น ไม่ retry; network/5xx → retry
5. เว้น 1 วินาทีระหว่างการจอง (ทีละใบ ไม่ยิงขนาน); จบด้วย exit code ≠ 0 ถ้ามีการจองที่ล้มเหลว

## สถานะใบจอง (`check-reserve`)

| status | ความหมาย |
|---|---|
| `WAIT` | จองแล้ว ยังไม่ชำระเงิน (มี `expire_at`) |
| `WAIT_APPROVE` | อัปสลิปแล้ว รอตรวจ |
| `PAID` | ชำระเงินเรียบร้อย |
| `CANCEL` | ยกเลิก/หมดอายุ — ไม่นับว่าจองแล้ว |

response: `{"status":"success","data":[{id, reservation_no, status, start_date, end_date, qty_people, service_total, expire_at, ...}]}` — ไม่เจอ = `data: []`

## เปิดจองหรือยัง

หน้าเว็บแสดงฟอร์มเมื่อ `GET /api/chiangdao/open` ตอบ 200 และ `data.open == true`.
ณ 2026-10-16 ตอบ HTTP 500 `Something broke!` (ยังไม่เปิด). `check-qty-people` ตอบว่าว่างแม้ยังไม่เปิด — ใช้แทนไม่ได้.

## `enc` คืออะไร

จาก JS ของเว็บ (chunk 9495):

```js
CryptoJS.AES.encrypt(
  JSON.stringify({ first_name, id_card_number, phone, key: "wlsfca16" }),  // ของคนแรก
  "wlsfca168upN]y[!"
).toString()
```

= OpenSSL format (`Salted__` + salt 8 byte, key/iv จาก EVP_BytesToKey-MD5, AES-256-CBC, PKCS7) → base64.
ค่าเปลี่ยนตาม first_name / id_card_number / phone ของคนแรก และ salt สุ่มทำให้ต่างทุกครั้งแม้ข้อมูลเดิม.

## ยังไม่รู้ / ข้อจำกัด

- ยังไม่เคยยิง `-submit` จริง (ระบบยังไม่เปิด) — response ของ `reservation/form` ยังไม่รู้ (log body ดิบไว้ แล้วยืนยันผลด้วย `check-reserve`)
- จองหลายคนในใบเดียวทำตาม JS ของเว็บ ยังไม่ได้ทดสอบจริง; จำนวนคนสูงสุดต่อใบยังไม่รู้
- หลังจองเว็บมี step อัปโหลดเอกสาร/สลิป (`upload-permission-doc-multi`, `upload-slip`) และ QR (`payment/qr`) — ยังไม่ทำ ต้องทำผ่านเว็บ
- ถ้าเว็บเปลี่ยน key ใน JS bundle ต้องอัปเดต `crypto.go`
