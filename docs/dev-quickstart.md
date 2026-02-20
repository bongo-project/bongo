# Dev Mode Quick Start

## Build and Run

```bash
go build -o bongo-go ./cmd/bongo-go
./bongo-go serve --dev
```

Dev mode starts a single-node server with ephemeral storage (deleted on shutdown)
and zero configuration required.

## Default Ports

| Service    | Port | Protocol          |
|------------|------|-------------------|
| HTTP       | 8080 | Health, internal   |
| SMTP MX    | 2525 | Inbound mail       |
| SMTP Sub   | 2587 | Submission (auth)  |
| IMAP       | 1143 | Plaintext/STARTTLS |
| IMAPS      | 1993 | Implicit TLS       |

## Test User

Dev mode auto-creates a test user on startup:

- **Email:** `admin@localhost`
- **Password:** `admin`

## Send a Test Email

Using [swaks](https://github.com/jetmore/swaks):

```bash
swaks --to admin@localhost \
      --from sender@example.com \
      --server 127.0.0.1:2525 \
      --header "Subject: Hello from swaks" \
      --body "This is a test message."
```

Or with `net/smtp` (Go stdlib):

```bash
echo -e "Subject: Test\r\n\r\nHello" | \
  go run -exec '' <<'EOF'
package main
import "net/smtp"
func main() {
    smtp.SendMail("127.0.0.1:2525", nil, "sender@example.com",
        []string{"admin@localhost"}, []byte("Subject: Test\r\n\r\nHello"))
}
EOF
```

## Connect Thunderbird

1. Open Thunderbird, go to **Account Settings > Account Actions > Add Mail Account**.
2. Enter:
   - Name: `Admin`
   - Email: `admin@localhost`
   - Password: `admin`
3. Click **Configure manually** and set:
   - **Incoming (IMAP):**
     - Server: `127.0.0.1`
     - Port: `1143`
     - SSL: None
     - Authentication: Normal password
   - **Outgoing (SMTP):**
     - Server: `127.0.0.1`
     - Port: `2587`
     - SSL: None
     - Authentication: Normal password
4. Click **Done** (accept any certificate warnings for self-signed certs).

## Verify

Check the health endpoint:

```bash
curl http://127.0.0.1:8080/health/ready
```

Returns `{"status":"ready"}` when the server is fully initialized.
