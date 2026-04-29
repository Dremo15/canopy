# OPEC Plugin — AI Agent Context

## What This Chain Does
OPEC is a fractional real-world asset (RWA) marketplace on Canopy Network.
Users can tokenize real-world assets (real estate, art, commodities), buy
fractional ownership, trade peer-to-peer, receive yield distributions, and
vote on DAO governance proposals. Native token: $OPEC.

## Transaction Types

| Type | Handler | Description |
|---|---|---|
| `send` | MessageSend | Standard token transfer |
| `tokenize_asset` | MessageTokenizeAsset | Register RWA, mint fractions |
| `buy_fraction` | MessageBuyFraction | Buy fractions from owner |
| `transfer_fraction` | MessageTransferFraction | P2P fraction transfer |
| `distribute_yield` | MessageDistributeYield | Owner pushes yield to holders |
| `cast_vote` | MessageCastVote | DAO governance vote |

## ContractConfig (contract/contract.go)
```go
SupportedTransactions: []string{
    "send",               // index 0
    "tokenize_asset",     // index 1
    "buy_fraction",       // index 2
    "transfer_fraction",  // index 3
    "distribute_yield",   // index 4
    "cast_vote",          // index 5
}
TransactionTypeUrls: []string{
    "type.googleapis.com/types.MessageSend",              // index 0
    "type.googleapis.com/types.MessageTokenizeAsset",     // index 1
    "type.googleapis.com/types.MessageBuyFraction",       // index 2
    "type.googleapis.com/types.MessageTransferFraction",  // index 3
    "type.googleapis.com/types.MessageDistributeYield",   // index 4
    "type.googleapis.com/types.MessageCastVote",          // index 5
}
```

## State Schema

| Prefix | Type | Key Construction |
|---|---|---|
| `0x01` | Account | `JoinLenPrefix(0x01, addr)` |
| `0x02` | Pool (fee pool) | `JoinLenPrefix(0x02, chainId_bytes)` |
| `0x07` | FeeParams | `JoinLenPrefix(0x07, "/f/")` |
| `0x10` | Asset | `JoinLenPrefix(0x10, assetId_uint64_bigendian)` |
| `0x11` | AssetCounter | `JoinLenPrefix(0x11, "/ac/")` |
| `0x12` | Holding | `JoinLenPrefix(0x12, holderAddr+assetId_bytes)` |
| `0x13` | AssetHolderIndex | `JoinLenPrefix(0x13, assetId_uint64_bigendian)` |
| `0x14` | HoldingIndex | `JoinLenPrefix(0x14, holderAddr)` |
| `0x15` | Proposal | `JoinLenPrefix(0x15, proposalId_uint64_bigendian)` |
| `0x16` | ProposalCounter | `JoinLenPrefix(0x16, "/pc/")` |
| `0x17` | VoteRecord | `JoinLenPrefix(0x17, voterAddr+proposalId_bytes)` |

## Business Rules
- **1.5% platform fee** on `tokenize_asset` (fractions) and `buy_fraction` (token amount) → treasury
- **Treasury address**: `4f50454354726561737572794164647231303031` (fixed 20-byte address)
- **Yield distribution**: proportional to fractions held; remainder stays with owner
- **Vote weight**: sum of all fractions held across all assets by the voter
- **One vote per address per proposal** — enforced via VoteRecord state
- **Owner-only**: only the asset owner can call `distribute_yield`

## Key Interfaces (contract/plugin.go)
- `StateRead(c, req)` → `(*PluginStateReadResponse, *PluginError)` — always check BOTH return values
- `StateWrite(c, req)` → `(*PluginStateWriteResponse, *PluginError)` — always check BOTH return values
- `FromAny(msg)` → `(proto.Message, *PluginError)` — deserializes google.protobuf.Any
- `JoinLenPrefix(parts ...[]byte)` → `[]byte` — builds length-prefixed state keys
- `Marshal(msg)` → `([]byte, *PluginError)`
- `Unmarshal(bytes, ptr)` → `*PluginError`

## Error Codes (contract/error.go)
Built-in codes 1–14 are reserved. OPEC custom codes start at 15+.
- `ErrInvalidAddress()` — code 12, address not 20 bytes
- `ErrInvalidAmount()` — code 13, zero or invalid amount
- `ErrInsufficientFunds()` — code 9, balance too low
- `ErrInvalidMessageCast()` — code 11, unknown message type
- `ErrTxFeeBelowStateLimit()` — code 14, fee too low

## Module Path
`github.com/canopy-network/go-plugin` (upstream template path — kept as-is)

## Files Never To Touch
- `contract/plugin.go` — socket protocol
- `main.go` — entry point
- `proto/plugin.proto` — FSM protocol
- `contract/tx.pb.go` — regenerated from tx.proto

## Files To Edit
- `contract/contract.go` — all application logic lives here
- `proto/tx.proto` — message type definitions
- `frontend/index.html` — single-file frontend, no build step

## Rebuild Sequence
```bash
# After editing tx.proto:
cd plugin/go/proto && ./_generate.sh

# After editing contract.go:
cd plugin/go && GOTOOLCHAIN=local go build -o go-plugin .

# Start chain:
cd ~/canopy && canopy start
```
