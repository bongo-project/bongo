# Bongo load test bundle (@VERSION@)

This bundle runs the Bongo load test on two Linux VMs: a server running Bongo
and a generator producing the load. It finds how much mail, how many clients
and how many connections one Bongo node handles, and what gives out first.

Everything runs on a made-up domain, `bongo.test` (RFC 6761: it never
resolves). The server relays all outgoing mail to a sink on the generator, so
nothing reaches the internet. The load tool refuses any target it was not
told about and any production name.

## Contents

| Path | What it is |
|---|---|
| `bin/bongo` | Bongo @VERSION@, linux/@ARCH@, from the official release (SHA-256 in `SHA256SUMS`) |
| `bin/loadgen` | The load generator, built from the @VERSION@ tag (`scripts/loadgen`) |
| `server/setup.sh` | Installs Bongo, the test config, a self-signed certificate and a systemd unit |
| `server/bulk-load.sh` | Writes test accounts and mail straight into an empty data directory |
| `server/egress-check.sh` | Proves the server reaches the sink and cannot reach port 25 on the internet |
| `generator/setup.sh` | Installs loadgen and raises the open-file and port limits |
| `generator/run.sh` | Runs each stage, records server and generator stats, writes reports, packs the results |
| `generator/loadtest.env.example` | Settings for `run.sh` |
| `docs/RUNBOOK.md` | The full runbook the scripts follow (`docs/load-testing.md` in the repo) |
| `docs/first-results.md` | Results from the first series on Hetzner cx23 VMs, for comparison |

## The VMs

| | Server | Generator |
|---|---|---|
| CPU and RAM | 2 vCPU, 4 GB to start, then 4/8 and 8/16 | 8 vCPU, 8 to 16 GB |
| Disks | 40 GB system, plus a 500 GB to 1 TB data disk mounted at `/var/lib/bongo` | 40 GB |
| OS | Debian 12/13 or Ubuntu 24.04 | same |
| Network | same bridge as the generator | 3 or 4 addresses on that bridge for the connection stage |

Outbound port 25 from the server is blocked at the firewall. The generator
needs SSH key access to the server as a user with passwordless sudo: `run.sh`
opens tunnels with it and records the server's CPU, memory and disk during
each stage.

The 2 vCPU / 4 GB server matches the first series, so the numbers compare
directly. Repeating a stage at a larger size shows whether a limit is CPU or
memory: if it does not move when they double, it is something else.

## Steps

### 1. Server

```sh
unzip @BUNDLE@.zip && cd @BUNDLE@
sudo ./server/setup.sh GENERATOR_IP:2525          # add --clamav to scan mail
```

The server is installed but not started. The session secret is generated into
`/etc/bongo/bongo.env` (root, mode 0600) and is never printed.

### 2. Generator

```sh
unzip @BUNDLE@.zip && cd @BUNDLE@
sudo ./generator/setup.sh                         # then log out and back in
cp generator/loadtest.env.example generator/loadtest.env
$EDITOR generator/loadtest.env                    # addresses, ssh user, machine description
```

For the connection stage, add the extra source addresses to the interface and
list them in `SOURCE_ADDRS`:

```sh
sudo ip addr add 10.0.0.11/24 dev eth0
sudo ip addr add 10.0.0.12/24 dev eth0
```

### 3. Load the data (on the server)

```sh
sudo ./server/bulk-load.sh 1500 50 1              # accounts, messages each, seed
```

This writes 1,500 accounts with 50 messages each and starts the server. The
same numbers and seed always give the same data. Copy the state file it names
to the generator as `generator/bulk.state` (mode 0600). It holds every test
account's password, so it stays out of the results.

If the data disk can be snapshotted, stop the server and snapshot it now, then
restore it before each run. Each run adds mail, so runs compare only from the
same starting data.

For a fresh store: `sudo systemctl stop bongo && sudo rm -rf /var/lib/bongo/data`,
then run `bulk-load.sh` again.

### 4. Pre-flight

On the generator, in tmux:

```sh
./generator/run.sh sink                           # leave running in its own window
./generator/run.sh tunnels
./generator/run.sh preflight
```

On the server, with one or two internet hosts on port 25 that you are allowed
to try (your own mail server, for example):

```sh
./server/egress-check.sh GENERATOR_IP:2525 YOUR_MX_IP:25
```

Every line must read `ok`, and the probe must read `blocked`. Do not start
load if it fails.

### 5. Stages (on the generator, in tmux)

| Command | Stage | Typical length |
|---|---|---|
| `./generator/run.sh ramp-mail` | Raise the mail rate until the server falls behind | up to `STAGE_DURATION` (4 h) |
| `./generator/run.sh ramp-mixed` | The same, with IMAP IDLE, IMAP, POP3 and DAV clients connected | up to 4 h |
| `./generator/run.sh connections` | Hold `CONNECTIONS` IMAP IDLE sessions | about 1 h per step |
| `./generator/run.sh soak` | `SOAK_RATE` messages a second for `SOAK_DURATION` | 24 to 72 h |

Each stage writes a new directory under `results/`. It holds:
- the loadgen summaries;
- the server's `/metrics`, scraped every few seconds;
- `vmstat` and `iostat` from the server, and `vmstat` from the generator;
- a record of both machines.

**When to stop a ramp:** press Ctrl-C when the server stops keeping up. Signs:
- accepted messages fall below the offered rate;
- SMTP latency climbs steeply;
- the probe starts counting messages lost.

Every process writes its summary on the way out. The runbook (section 7) has
the stop conditions and the metrics to watch. In the first series, the limit
on 2 vCPU was search indexing, at about 16 messages a second. The default
ramp therefore starts at 5 a second and adds 5 every 10 minutes.

Exit 3 from the SMTP load means the generator saturated, and that run did not
measure the server. `run.sh` prints what each exit code means.

For connections, raise `CONNECTIONS` between runs: 5,000, 15,000, 25,000 and
beyond. On 2 vCPU the limit was the rate of new logins (TLS plus bcrypt),
about 16 a second, not the number of open connections.

Restore the snapshot between stages if you have one.

The runbook also describes these stages, which `run.sh` does not wrap:
- account scale (7.5): a bigger `bulk-load.sh` and `ramp-mixed` again;
- storage scale (7.6);
- failure cases (7.8).

### 6. Reports and results

```sh
./generator/run.sh report results/<stage directory>   # writes report.md and report.json
```

When you are finished, stop the sink with Ctrl-C (it writes its summary into
`results/`), then:

```sh
./generator/run.sh collect
```

`collect` writes `bongo-loadtest-results-<host>-<time>.tar.gz`, which is the
file to send back. It leaves out:
- the state file;
- the session secret;
- `loadtest.env`, which names the SSH user;
- the copy of the server config. The reports record its SHA-256.

### 7. Afterwards

Delete these from the generator:
- `generator/bulk.state`;
- `generator/session-secret`, if `run.sh seed` was used.

Then delete both VMs and any snapshots.

## Notes

- **Results are preliminary until a stage has run twice** on the same release
  and machine and agrees within 10%.
- **ClamAV:** with `--clamav`, clamd needs its signatures before it starts.
  Check that `/var/run/clamav/clamd.ctl` exists before the first run. With
  clamd stopped, mail is quarantined rather than delivered. The first series
  ran with ClamAV off.
- **Clocks:** keep both VMs' clocks in sync. The control listener, used only by
  `run.sh seed`, refuses requests more than 90 seconds off.
- **What not to expose:** `/metrics` and the control listener answer only on
  loopback and private addresses. `run.sh` reaches them through SSH tunnels.
  Do not open them up.
