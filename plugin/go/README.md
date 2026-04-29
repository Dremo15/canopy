# OPEC — Fractional Real-World Asset Marketplace

OPEC is an application-specific blockchain built on [Canopy Network](https://github.com/canopy-network/canopy). It enables fractional ownership of real-world assets (RWAs) — real estate, art, commodities, and more — using the native **$OPEC** token.

---

## Features

| Feature | Description |
|---|---|
| 🏛️ Tokenize Assets | Register any RWA on-chain and mint N fractions |
| 💸 Buy Fractions | Purchase fractions from the asset owner with $OPEC |
| 🔄 P2P Transfer | Trade fractions peer-to-peer on the secondary market |
| 🌾 Yield Distribution | Asset owners push yield proportionally to all holders |
| 🗳️ DAO Governance | Vote on proposals — weight equals fractions held |
| 🏦 Treasury | 1.5% platform fee on tokenization and purchases |

---

## Transaction Types

### `tokenize_asset`
Registers a new real-world asset and mints fractions. 1.5% of fractions go to the OPEC treasury automatically.

```json
{
  "type": "tokenize_asset",
  "ownerAddress": "<20-byte address as base64>",
  "assetName": "Downtown Office Building",
  "assetType": "real_estate",
  "metadataURI": "https://example.com/asset/1",
  "totalFractions": 10000,
  "fractionPrice": 1000000
}
```

### `buy_fraction`
Purchases fractions from the asset owner. Cost = `quantity × fractionPrice`. 1.5% platform fee goes to treasury.

```json
{
  "type": "buy_fraction",
  "buyerAddress": "<20-byte address as base64>",
  "assetId": 1,
  "quantity": 100
}
```

### `transfer_fraction`
Sends fractions peer-to-peer. No platform fee — only the tx fee applies.

```json
{
  "type": "transfer_fraction",
  "fromAddress": "<20-byte address as base64>",
  "toAddress": "<20-byte address as base64>",
  "assetId": 1,
  "quantity": 50
}
```

### `distribute_yield`
Caller must be the asset owner. Distributes `totalYield` proportionally to all fraction holders. Remainder stays with the owner.

```json
{
  "type": "distribute_yield",
  "ownerAddress": "<20-byte address as base64>",
  "assetId": 1,
  "totalYield": 5000000
}
```

### `cast_vote`
Submits a DAO governance vote. Vote weight = total fractions held across all assets. Each address may only vote once per proposal.

```json
{
  "type": "cast_vote",
  "voterAddress": "<20-byte address as base64>",
  "proposalId": 1,
  "approve": true
}
```

---

## State Keys

| Prefix | Type | Key |
|---|---|---|
| `0x01` | Account | `holderAddr` |
| `0x02` | Pool (fee pool) | `chainId` |
| `0x07` | FeeParams | `/f/` |
| `0x10` | Asset | `assetId` |
| `0x11` | AssetCounter | `/ac/` |
| `0x12` | Holding | `holderAddr + assetId` |
| `0x13` | AssetHolderIndex | `assetId` |
| `0x14` | HoldingIndex | `holderAddr` |
| `0x15` | Proposal | `proposalId` |
| `0x16` | ProposalCounter | `/pc/` |
| `0x17` | VoteRecord | `voterAddr + proposalId` |

---

## Running Locally

```bash
# 1. Clone and build Canopy
git clone https://github.com/Dremo15/canopy.git
cd canopy
make build/canopy

# 2. Build the plugin
cd plugin/go
make build

# 3. Generate config
canopy start   # press Enter twice for no password, then Ctrl+C

# 4. Set plugin in ~/.canopy/config.json
#    "plugin": "go"

# 5. Start the chain
canopy start
```

### Expose ports (Cloudflare Tunnel)
```bash
cloudflared tunnel --url http://localhost:8080   # frontend
cloudflared tunnel --url http://localhost:50002  # public RPC
cloudflared tunnel --url http://localhost:50003  # admin RPC
```

### Serve the frontend
```bash
python3 -m http.server 8080 --directory plugin/go/frontend
```

---

## RPC Ports

| Port | Purpose |
|---|---|
| `50002` | Public RPC — submit transactions, query state |
| `50003` | Admin RPC — keystore, key management |

---

## Token Economics

- **Symbol:** $OPEC
- **Platform fee:** 1.5% on `tokenize_asset` (fractions) and `buy_fraction` (token amount)
- **Treasury address:** fixed 20-byte address receiving all platform fees
- **Yield:** fully on-chain, distributed by asset owners proportionally

---

## Built With

- [Canopy Network](https://github.com/canopy-network/canopy) — appchain infrastructure
- Go 1.25
- Protocol Buffers
- BLS12-381 signatures

---

## Contest

Built for the **Canopy Network Vibe Code Contest**.
