# 🏛️ PROPEX — Fractional RWA Ownership on Canopy

> **One-line pitch:** Propex lets anyone buy fractional ownership of real-world assets — real estate, art, commodities — onchain using CNPY, with built-in yield distribution and DAO governance.

Built with **Canopy Templates (Go)** + **Claude AI** for the Vibe Code competition.

---

## What Is Propex?

Propex is a fractional real-world asset (RWA) marketplace running as an application-specific blockchain on Canopy Network. Asset owners tokenize physical assets into fractions. Anyone with CNPY can buy in, earn yield, trade on the secondary market, and vote on governance proposals — all onchain.

**Core features:**
- List any real-world asset (real estate, art, commodities, equity, infrastructure)
- Buy fractions with CNPY — as little as 1 fraction
- Secondary market: list and trade fractions peer-to-peer
- Yield distribution: asset owners distribute income proportionally to all holders
- DAO governance: fraction holders vote on asset proposals (weight = fractions held)
- 1.5% platform fee on all trades flows to the Propex treasury

---

## Repo Structure

```
canopy-Dremo/
├── plugin/
│   ├── plugin.go        ← Canopy core plugin (upstream)
│   └── propex.go        ← Propex FSM — all onchain logic (Go)
├── frontend/
│   └── index.html       ← Full dApp UI (runs locally, no build needed)
├── client/
│   └── canopy-client.ts ← TypeScript RPC client library
└── README.md
```

---

## Run Locally on Termux

### Step 1 — Install dependencies

```bash
pkg update && pkg upgrade
pkg install golang git make
```

### Step 2 — Clone the repo

```bash
git clone https://github.com/Dremo15/canopy-Dremo.git
cd canopy-Dremo
```

### Step 3 — Build the Canopy binary

```bash
make build/canopy-full
```

> If `make` gives errors about missing tools, run: `pkg install binutils`

### Step 4 — Start a local node

```bash
./canopy start
```

The node starts an RPC server at `http://localhost:50832` automatically.

### Step 5 — Run the frontend

Open a second Termux session and serve the frontend:

```bash
cd canopy-Dremo/frontend
python3 -m http.server 8080
```

Then open your browser and go to:
```
http://localhost:8080
```

> **Demo mode:** If the Canopy node is not running, the frontend automatically falls back to demo mode — all UI interactions still work with local mock data. Transactions that hit the node show real results; others display `[Demo]` in the notification.

---

## Plugin Transactions

| Method | Description |
|---|---|
| `ListAsset` | Tokenize a real-world asset into fractions |
| `ApproveAsset` | Platform verifier activates a pending asset |
| `BuyFractions` | Purchase fractions from the primary pool |
| `CreateListing` | List fractions on the secondary market |
| `FillListing` | Buy fractions from a secondary listing |
| `CancelListing` | Cancel your active secondary listing |
| `DistributeYield` | Asset owner distributes income to all holders |
| `ClaimYield` | Holder claims accumulated yield as CNPY |
| `CreateProposal` | Fraction holder creates a governance proposal |
| `CastVote` | Vote on an active proposal (weight = fractions held) |

---

## Constants

| Parameter | Value |
|---|---|
| Min fractions per asset | 100 |
| Max fractions per asset | 1,000,000 |
| Min fraction price | 0.1 CNPY (100,000 nCNPY) |
| Platform fee | 1.5% on all trades |
| Governance vote window | ~1 week (2,016 blocks) |
| Quorum required | 10% of sold fractions |

---

## RPC Endpoint

The frontend connects to your **local Canopy node** at:
```
http://localhost:50832
```

Plugin ID: `propex-rwa-v1`

All transactions follow the pattern:
```
POST http://localhost:50832/v1/plugin/propex-rwa-v1/{MethodName}
```

---

## Built With

- [Canopy Network](https://canopynetwork.org) — Go Template
- [Claude AI](https://claude.ai) — Architecture + code generation
- Vanilla HTML/CSS/JS frontend — zero dependencies, runs anywhere

---

## Submission

- **GitHub:** https://github.com/Dremo15/canopy-Dremo
- **X:** @MakDaVeli
- **Discord:** Makaveli
 
