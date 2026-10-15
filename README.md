# chiangdao-reservation

จองเขตรักษาพันธุ์สัตว์ป่าเชียงดาว (wildlifesanctuaryfca16.com) อัตโนมัติจากไฟล์ CSV

## ใช้งาน

```bash
go build -o chiangdao-reservation .
cp people.example.csv people.csv   # แก้ข้อมูลจริง (people.csv อยู่ใน .gitignore)

./chiangdao-reservation -csv people.csv                     # dry-run: แสดงสิ่งที่จะส่ง ไม่ยิงจริง
./chiangdao-reservation -csv people.csv -submit             # จองจริงทันที
./chiangdao-reservation -csv people.csv -submit -at "2026-10-17 09:00:00"   # รอถึงเวลา (เวลาไทย) แล้วค่อยยิง
./chiangdao-reservation -csv people.csv -submit -group A    # จองเฉพาะกลุ่ม A
./chiangdao-reservation -csv people.csv -check              # ดูสถานะการจองของทุกกลุ่ม (ไม่จอง)
```

| flag | default | ความหมาย |
|---|---|---|
| `-csv` | `people.csv` | ไฟล์ข้อมูล |
| `-submit` | `false` | ไม่ใส่ = dry-run เท่านั้น |
| `-check` | `false` | ดูสถานะการจองอย่างเดียว |
| `-at` | ว่าง | เวลาเริ่มยิง `YYYY-MM-DD HH:MM:SS` (Asia/Bangkok) |
| `-group` | ว่าง | จองเฉพาะกลุ่มนี้ |
| `-retries` | `5` | จำนวนครั้งสูงสุดต่อการจอง (retry เฉพาะ network error / HTTP 5xx) |
| `-retry-delay` | `2s` | เว้นระหว่าง retry |

## รูปแบบ CSV

1 แถว = 1 คน. แถวที่ `group` เดียวกัน = การจองเดียวกัน (`qty_people` = จำนวนแถวในกลุ่ม).
คอลัมน์ระดับการจองอ่านจาก **แถวแรกของกลุ่ม** เท่านั้น. คนแรกของกลุ่ม = ผู้จองหลัก (ใช้สร้าง `enc`).

| คอลัมน์ | ระดับ | บังคับ | หมายเหตุ |
|---|---|---|---|
| `group` | - | ✔ | ชื่อกลุ่มอะไรก็ได้ |
| `start_date`, `end_date` | การจอง | ✔ (แถวแรก) | `YYYY-MM-DD` |
| `email` | การจอง | | |
| `service1`, `service2` | การจอง | | JSON ตามเว็บ, ว่าง = ค่าจาก curl ตัวอย่าง |
| `code`, `bank`, `bank_acc_no`, `bank_acc_name` | การจอง | | ว่างได้ |
| `prefix`, `first_name`, `last_name`, `phone` | คน | ✔ (ยกเว้น prefix) | |
| `birth_date` | คน | ✔ | `YYYY-MM-DD` → แปลงเป็น `Thu Apr 03 1997 00:00:00 GMT+0700 (Indochina Time)` เหมือน browser |
| `id_card_number` / `passport` | คน | อย่างใดอย่างหนึ่ง | |

## ลำดับการทำงาน (ต่อ 1 กลุ่ม)

1. `GET /api/chiangdao/reservation/check-reserve?phone=&id_card_number=` (เบอร์ + บัตร/passport ของคนแรก) → ถ้ามีใบจองวัน `start_date` เดียวกันที่ไม่ใช่ `CANCEL` อยู่แล้ว ข้าม (กันจองซ้ำตอนรันใหม่)
2. `GET /api/chiangdao/reservation/check-qty-people?start_date=` → ถ้าที่ว่าง (`qty_reserve`) < จำนวนคน ข้ามกลุ่มนี้
3. `POST /api/chiangdao/reservation/form` (multipart, ลำดับ field เหมือน JS ของเว็บ, สร้าง `enc` ใหม่ทุกครั้ง)
4. `status == "success"` → ยืนยันด้วย `check-reserve` อีกรอบ (สูงสุด 3 ครั้ง ห่าง 2 วินาที) แล้ว log `BOOKED ✔` + เลขใบจอง/สถานะ/ยอด/หมดอายุ
   HTTP 4xx/ธุรกิจ error → หยุดกลุ่มนั้น ไม่ retry; network/5xx → retry
5. เว้น 1 วินาทีระหว่างกลุ่ม; จบด้วย exit code ≠ 0 ถ้ามีกลุ่มที่ล้มเหลว

## สถานะใบจอง (`check-reserve`)

| status | ความหมาย |
|---|---|
| `WAIT` | จองแล้ว ยังไม่ชำระเงิน (มี `expire_at`) |
| `WAIT_APPROVE` | อัปสลิปแล้ว รอตรวจ |
| `PAID` | ชำระเงินเรียบร้อย |
| `CANCEL` | ยกเลิก/หมดอายุ — ไม่นับว่าจองแล้ว |

response: `{"status":"success","data":[{id, reservation_no, status, start_date, end_date, qty_people, service_total, expire_at, ...}]}` — ไม่เจอ = `data: []`

## `enc` คืออะไร

จาก JS ของเว็บ (chunk 9495):

```js
CryptoJS.AES.encrypt(
  JSON.stringify({ first_name, id_card_number, phone, key: "wlsfca16" }),  // ของคนแรกในกลุ่ม
  "wlsfca168upN]y[!"
).toString()
```

= OpenSSL format (`Salted__` + salt 8 byte, key/iv จาก EVP_BytesToKey-MD5, AES-256-CBC, PKCS7) → base64.
ค่าเปลี่ยนตาม first_name / id_card_number / phone ของคนแรก และ salt สุ่มทำให้ต่างทุกครั้งแม้ข้อมูลเดิม.

## ยังไม่รู้ / ข้อจำกัด

- ยังไม่เคยยิง `-submit` จริง — response ของ `reservation/form` ยังไม่รู้ (โปรแกรม log body ดิบไว้ แล้วยืนยันผลด้วย `check-reserve`)
- หลังจองเว็บมี step อัปโหลดเอกสาร/สลิป (`upload-permission-doc-multi`, `upload-slip`) และ QR (`payment/qr`) — ยังไม่ทำ ต้องทำผ่านเว็บ
- ความหมาย service id ส่วนใหญ่ยังไม่ได้ map (ดูได้จาก `GET /api/chiangdao/service/prepayment?qty_people=N&start_date=`)
- ถ้าเว็บเปลี่ยน key ใน JS bundle ต้องอัปเดต `crypto.go`
