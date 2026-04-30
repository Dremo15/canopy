package contract

import (
	"bytes"
	"fmt"
	"encoding/binary"
	"log"
	"math/rand"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
)

// ── Plugin configuration ───────────────────────────────────────────────────────

var ContractConfig = &PluginConfig{
	Name:    "go_plugin_contract",
	Id:      1,
	Version: 1,
	SupportedTransactions: []string{
		"send",
		"tokenize_asset",
		"buy_fraction",
		"transfer_fraction",
		"distribute_yield",
		"cast_vote",
	},
	TransactionTypeUrls: []string{
		"type.googleapis.com/types.MessageSend",
		"type.googleapis.com/types.MessageTokenizeAsset",
		"type.googleapis.com/types.MessageBuyFraction",
		"type.googleapis.com/types.MessageTransferFraction",
		"type.googleapis.com/types.MessageDistributeYield",
		"type.googleapis.com/types.MessageCastVote",
	},
	EventTypeUrls: nil,
}

func init() {
	file_account_proto_init()
	file_event_proto_init()
	file_plugin_proto_init()
	file_tx_proto_init()

	var fds [][]byte
	for _, file := range []protoreflect.FileDescriptor{
		anypb.File_google_protobuf_any_proto,
		File_account_proto, File_event_proto, File_plugin_proto, File_tx_proto,
	} {
		fd, _ := proto.Marshal(protodesc.ToFileDescriptorProto(file))
		fds = append(fds, fd)
	}
	ContractConfig.FileDescriptorProtos = fds
}

// ── Contract struct ────────────────────────────────────────────────────────────

type Contract struct {
	Config        Config
	FSMConfig     *PluginFSMConfig
	plugin        *Plugin
	fsmId         uint64
	currentHeight uint64 // captured in BeginBlock
}

// ── Treasury ───────────────────────────────────────────────────────────────────

// opecTreasury is the 20-byte address that receives 1.5% platform fees.
// Derived from the ASCII bytes of "OPECTreasuryAddr1" padded to 20 bytes.
var opecTreasury = []byte{
	0x4f, 0x50, 0x45, 0x43, 0x54, 0x72, 0x65, 0x61,
	0x73, 0x75, 0x72, 0x79, 0x41, 0x64, 0x64, 0x72,
	0x31, 0x30, 0x30, 0x31,
}

// ── State key prefixes ─────────────────────────────────────────────────────────
// Reserved by base template (do not reuse):
//   0x01 → Account
//   0x02 → Pool (fee pool)
//   0x07 → FeeParams
// OPEC additions:
//   0x10 → Asset
//   0x11 → AssetCounter
//   0x12 → Holding       (holderAddr + assetId)
//   0x13 → AssetHolderIndex (assetId → []holderAddr)
//   0x14 → HoldingIndex  (holderAddr → []assetId)
//   0x15 → Proposal
//   0x16 → ProposalCounter
//   0x17 → VoteRecord    (voterAddr + proposalId)

var (
	accountPrefix          = []byte{0x01}
	poolPrefix             = []byte{0x02}
	paramsPrefix           = []byte{0x07}
	assetPrefix            = []byte{0x10}
	assetCounterPrefix     = []byte{0x11}
	holdingPrefix          = []byte{0x12}
	assetHolderIndexPrefix = []byte{0x13}
	holdingIndexPrefix     = []byte{0x14}
	proposalPrefix         = []byte{0x15}
	proposalCounterPrefix  = []byte{0x16}
	voteRecordPrefix       = []byte{0x17}
)

// ── Key constructors ───────────────────────────────────────────────────────────

func KeyForAccount(addr []byte) []byte {
	return JoinLenPrefix(accountPrefix, addr)
}

func KeyForFeeParams() []byte {
	return JoinLenPrefix(paramsPrefix, []byte("/f/"))
}

func KeyForFeePool(chainId uint64) []byte {
	return JoinLenPrefix(poolPrefix, u64b(chainId))
}

func KeyForAsset(assetId uint64) []byte {
	return JoinLenPrefix(assetPrefix, u64b(assetId))
}

func KeyForAssetCounter() []byte {
	return JoinLenPrefix(assetCounterPrefix, []byte("/ac/"))
}

// KeyForHolding: composite key = prefix + holderAddr + assetId bytes
func KeyForHolding(holderAddr []byte, assetId uint64) []byte {
	composite := make([]byte, len(holderAddr)+8); copy(composite, holderAddr); copy(composite[len(holderAddr):], u64b(assetId))
	return JoinLenPrefix(holdingPrefix, composite)
}

func KeyForAssetHolderIndex(assetId uint64) []byte {
	return JoinLenPrefix(assetHolderIndexPrefix, u64b(assetId))
}

func KeyForHoldingIndex(holderAddr []byte) []byte {
	return JoinLenPrefix(holdingIndexPrefix, holderAddr)
}

func KeyForProposal(proposalId uint64) []byte {
	return JoinLenPrefix(proposalPrefix, u64b(proposalId))
}

func KeyForProposalCounter() []byte {
	return JoinLenPrefix(proposalCounterPrefix, []byte("/pc/"))
}

// KeyForVoteRecord: composite key = prefix + voterAddr + proposalId bytes
func KeyForVoteRecord(voterAddr []byte, proposalId uint64) []byte {
	composite := make([]byte, len(voterAddr)+8); copy(composite, voterAddr); copy(composite[len(voterAddr):], u64b(proposalId))
	return JoinLenPrefix(voteRecordPrefix, composite)
}

func u64b(u uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, u)
	return b
}

// ── Lifecycle ──────────────────────────────────────────────────────────────────

func (c *Contract) Genesis(_ *PluginGenesisRequest) *PluginGenesisResponse {
	return &PluginGenesisResponse{}
}

func (c *Contract) BeginBlock(req *PluginBeginRequest) *PluginBeginResponse {
	c.currentHeight = req.Height
	return &PluginBeginResponse{}
}

func (c *Contract) CheckTx(req *PluginCheckRequest) *PluginCheckResponse {
	// Safe fee params read — loop over results, never index directly
	feeQId := rand.Uint64()
	resp, pluginErr := c.plugin.StateRead(c, &PluginStateReadRequest{
		Keys: []*PluginKeyRead{{QueryId: feeQId, Key: KeyForFeeParams()}},
	})
	if pluginErr != nil {
		return &PluginCheckResponse{Error: pluginErr}
	}
	if resp.Error != nil {
		return &PluginCheckResponse{Error: resp.Error}
	}
	minFees := new(FeeParams)
	for _, r := range resp.Results {
		if r.QueryId == feeQId && len(r.Entries) > 0 {
			if uerr := Unmarshal(r.Entries[0].Value, minFees); uerr != nil {
				return &PluginCheckResponse{Error: uerr}
			}
		}
	}
	if req.Tx.Fee < minFees.SendFee {
		return &PluginCheckResponse{Error: ErrTxFeeBelowStateLimit()}
	}

	msg, pluginErr := msgFromAny(req.Tx.Msg)
	if pluginErr != nil {
		return &PluginCheckResponse{Error: pluginErr}
	}
	switch x := msg.(type) {
	case *MessageSend:
		return c.CheckMessageSend(x)
	case *MessageTokenizeAsset:
		return c.CheckMessageTokenizeAsset(x)
	case *MessageBuyFraction:
		return c.CheckMessageBuyFraction(x)
	case *MessageTransferFraction:
		return c.CheckMessageTransferFraction(x)
	case *MessageDistributeYield:
		return c.CheckMessageDistributeYield(x)
	case *MessageCastVote:
		return c.CheckMessageCastVote(x)
	default:
		return &PluginCheckResponse{Error: ErrInvalidMessageCast()}
	}
}

func (c *Contract) DeliverTx(req *PluginDeliverRequest) *PluginDeliverResponse {
	msg, pluginErr := msgFromAny(req.Tx.Msg)
	if pluginErr != nil {
		return &PluginDeliverResponse{Error: pluginErr}
	}
	switch x := msg.(type) {
	case *MessageSend:
		return c.DeliverMessageSend(x, req.Tx.Fee)
	case *MessageTokenizeAsset:
		return c.DeliverMessageTokenizeAsset(x, req.Tx.Fee)
	case *MessageBuyFraction:
		return c.DeliverMessageBuyFraction(x, req.Tx.Fee)
	case *MessageTransferFraction:
		return c.DeliverMessageTransferFraction(x, req.Tx.Fee)
	case *MessageDistributeYield:
		return c.DeliverMessageDistributeYield(x, req.Tx.Fee)
	case *MessageCastVote:
		return c.DeliverMessageCastVote(x, req.Tx.Fee)
	default:
		return &PluginDeliverResponse{Error: ErrInvalidMessageCast()}
	}
}

func (c *Contract) EndBlock(_ *PluginEndRequest) *PluginEndResponse {
	return &PluginEndResponse{}
}

// ── helpers ────────────────────────────────────────────────────────────────────

// stateRead1 reads a single key and returns the raw bytes (nil if not found).
func (c *Contract) stateRead1(key []byte) ([]byte, *PluginError) {
	qId := rand.Uint64()
	resp, err := c.plugin.StateRead(c, &PluginStateReadRequest{
		Keys: []*PluginKeyRead{{QueryId: qId, Key: key}},
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	for _, r := range resp.Results {
		if r.QueryId == qId && len(r.Entries) > 0 {
			return r.Entries[0].Value, nil
		}
	}
	return nil, nil
}

// stateWrite writes a batch of sets and deletes atomically, checking both error paths.
func (c *Contract) stateWrite(sets []*PluginSetOp, deletes []*PluginDeleteOp) *PluginError {
	wresp, err := c.plugin.StateWrite(c, &PluginStateWriteRequest{Sets: sets, Deletes: deletes})
	if err != nil {
		return err
	}
	if wresp.Error != nil {
		return wresp.Error
	}
	return nil
}

// mustMarshal marshals a proto message or returns a PluginError.
func mustMarshal(msg proto.Message) ([]byte, *PluginError) {
	b, err := Marshal(msg)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// platformFee calculates the 1.5% OPEC platform fee (minimum 1 if amount > 0).
func platformFee(amount uint64) uint64 {
	fee := amount * 15 / 1000
	if fee == 0 && amount > 0 {
		fee = 1
	}
	return fee
}

// ── send ───────────────────────────────────────────────────────────────────────

func (c *Contract) CheckMessageSend(msg *MessageSend) *PluginCheckResponse {
	if len(msg.FromAddress) != 20 {
		return &PluginCheckResponse{Error: ErrInvalidAddress()}
	}
	if len(msg.ToAddress) != 20 {
		return &PluginCheckResponse{Error: ErrInvalidAddress()}
	}
	if msg.Amount == 0 {
		return &PluginCheckResponse{Error: ErrInvalidAmount()}
	}
	return &PluginCheckResponse{Recipient: msg.ToAddress, AuthorizedSigners: [][]byte{msg.FromAddress}}
}

func (c *Contract) DeliverMessageSend(msg *MessageSend, fee uint64) *PluginDeliverResponse {
	log.Printf("DeliverMessageSend: from=%x to=%x amount=%d fee=%d", msg.FromAddress, msg.ToAddress, msg.Amount, fee)
	fromQId, toQId, feeQId := rand.Uint64(), rand.Uint64(), rand.Uint64()
	fromKey := KeyForAccount(msg.FromAddress)
	toKey := KeyForAccount(msg.ToAddress)
	feePoolKey := KeyForFeePool(c.Config.ChainId)

	resp, err := c.plugin.StateRead(c, &PluginStateReadRequest{
		Keys: []*PluginKeyRead{
			{QueryId: fromQId, Key: fromKey},
			{QueryId: toQId, Key: toKey},
			{QueryId: feeQId, Key: feePoolKey},
		},
	})
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if resp.Error != nil {
		return &PluginDeliverResponse{Error: resp.Error}
	}

	from, to, feePool := new(Account), new(Account), new(Pool)
	var fromBytes, toBytes, feePoolBytes []byte
	for _, r := range resp.Results {
		if len(r.Entries) == 0 {
			continue
		}
		switch r.QueryId {
		case fromQId:
			fromBytes = r.Entries[0].Value
		case toQId:
			toBytes = r.Entries[0].Value
		case feeQId:
			feePoolBytes = r.Entries[0].Value
		}
	}

	if uerr := Unmarshal(fromBytes, from); uerr != nil {
		return &PluginDeliverResponse{Error: uerr}
	}
	if uerr := Unmarshal(toBytes, to); uerr != nil {
		return &PluginDeliverResponse{Error: uerr}
	}
	if uerr := Unmarshal(feePoolBytes, feePool); uerr != nil {
		return &PluginDeliverResponse{Error: uerr}
	}

	amountToDeduct := msg.Amount + fee
	if from.Amount < amountToDeduct {
		return &PluginDeliverResponse{Error: ErrInsufficientFunds()}
	}
	if bytes.Equal(fromKey, toKey) {
		to = from
	}
	from.Amount -= amountToDeduct
	feePool.Amount += fee
	to.Amount += msg.Amount

	fromBytes, merr := mustMarshal(from)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	toBytes, merr = mustMarshal(to)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	feePoolBytes, merr = mustMarshal(feePool)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}

	sets := []*PluginSetOp{
		{Key: feePoolKey, Value: feePoolBytes},
		{Key: toKey, Value: toBytes},
	}
	var deletes []*PluginDeleteOp
	if from.Amount == 0 {
		deletes = append(deletes, &PluginDeleteOp{Key: fromKey})
	} else {
		sets = append(sets, &PluginSetOp{Key: fromKey, Value: fromBytes})
	}
	if werr := c.stateWrite(sets, deletes); werr != nil {
		return &PluginDeliverResponse{Error: werr}
	}
	return &PluginDeliverResponse{}
}

// ── tokenize_asset ─────────────────────────────────────────────────────────────

func (c *Contract) CheckMessageTokenizeAsset(msg *MessageTokenizeAsset) *PluginCheckResponse {
	if len(msg.OwnerAddress) != 20 {
		return &PluginCheckResponse{Error: ErrInvalidAddress()}
	}
	if msg.AssetName == "" || len(msg.AssetName) > 128 {
		return &PluginCheckResponse{Error: ErrInvalidAmount()}
	}
	if msg.TotalFractions < 2 {
		return &PluginCheckResponse{Error: ErrInvalidAmount()}
	}
	if msg.FractionPrice == 0 {
		return &PluginCheckResponse{Error: ErrInvalidAmount()}
	}
	return &PluginCheckResponse{AuthorizedSigners: [][]byte{msg.OwnerAddress}}
}

func (c *Contract) DeliverMessageTokenizeAsset(msg *MessageTokenizeAsset, fee uint64) *PluginDeliverResponse {
	log.Printf("DeliverMessageTokenizeAsset: owner=%x name=%s fractions=%d price=%d", msg.OwnerAddress, msg.AssetName, msg.TotalFractions, msg.FractionPrice)

	height := c.currentHeight

	// Read owner account, asset counter, fee pool, treasury account
	ownerQId, counterQId, feePoolQId, treasuryQId := rand.Uint64(), rand.Uint64(), rand.Uint64(), rand.Uint64()
	resp, err := c.plugin.StateRead(c, &PluginStateReadRequest{
		Keys: []*PluginKeyRead{
			{QueryId: ownerQId, Key: KeyForAccount(msg.OwnerAddress)},
			{QueryId: counterQId, Key: KeyForAssetCounter()},
			{QueryId: feePoolQId, Key: KeyForFeePool(c.Config.ChainId)},
			{QueryId: treasuryQId, Key: KeyForAccount(opecTreasury)},
		},
	})
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if resp.Error != nil {
		return &PluginDeliverResponse{Error: resp.Error}
	}

	owner, feePool, treasury := new(Account), new(Pool), new(Account)
	counter := new(AssetCounter)
	var ownerBytes, feePoolBytes, treasuryBytes []byte

	for _, r := range resp.Results {
		if len(r.Entries) == 0 {
			continue
		}
		switch r.QueryId {
		case ownerQId:
			ownerBytes = r.Entries[0].Value
		case counterQId:
			if uerr := Unmarshal(r.Entries[0].Value, counter); uerr != nil {
				return &PluginDeliverResponse{Error: uerr}
			}
		case feePoolQId:
			feePoolBytes = r.Entries[0].Value
		case treasuryQId:
			treasuryBytes = r.Entries[0].Value
		}
	}

	if uerr := Unmarshal(ownerBytes, owner); uerr != nil {
		return &PluginDeliverResponse{Error: uerr}
	}
	if uerr := Unmarshal(feePoolBytes, feePool); uerr != nil {
		return &PluginDeliverResponse{Error: uerr}
	}
	if uerr := Unmarshal(treasuryBytes, treasury); uerr != nil {
		return &PluginDeliverResponse{Error: uerr}
	}

	if owner.Amount < fee {
		return &PluginDeliverResponse{Error: ErrInsufficientFunds()}
	}
	owner.Amount -= fee
	feePool.Amount += fee

	// Assign new asset ID
	counter.Count++
	assetId := counter.Count

	// 1.5% of fractions → treasury, rest → owner
	treasuryFractions := platformFee(msg.TotalFractions)
	ownerFractions := msg.TotalFractions - treasuryFractions

	asset := &Asset{
		Id:             assetId,
		OwnerAddress:   msg.OwnerAddress,
		AssetName:      msg.AssetName,
		AssetType:      msg.AssetType,
		MetadataUri:    msg.MetadataUri,
		TotalFractions: msg.TotalFractions,
		FractionPrice:  msg.FractionPrice,
		CreatedHeight:  height,
	}
	ownerHolding := &Holding{HolderAddress: msg.OwnerAddress, AssetId: assetId, Quantity: ownerFractions}
	treasuryHolding := &Holding{HolderAddress: opecTreasury, AssetId: assetId, Quantity: treasuryFractions}
	ownerHoldingIndex := &HoldingIndex{AssetIds: []uint64{assetId}}
	treasuryHoldingIndex := &HoldingIndex{AssetIds: []uint64{assetId}}
	holderIndex := &AssetHolderIndex{HolderAddresses: [][]byte{msg.OwnerAddress, opecTreasury}}

	// Marshal all
	assetB, merr := mustMarshal(asset)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	counterB, merr := mustMarshal(counter)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	ownerHoldingB, merr := mustMarshal(ownerHolding)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	treasuryHoldingB, merr := mustMarshal(treasuryHolding)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	ownerHoldingIndexB, merr := mustMarshal(ownerHoldingIndex)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	treasuryHoldingIndexB, merr := mustMarshal(treasuryHoldingIndex)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	holderIndexB, merr := mustMarshal(holderIndex)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	ownerBytes, merr = mustMarshal(owner)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	feePoolBytes, merr = mustMarshal(feePool)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}

	sets := []*PluginSetOp{
		{Key: KeyForAsset(assetId), Value: assetB},
		{Key: KeyForAssetCounter(), Value: counterB},
		{Key: KeyForHolding(msg.OwnerAddress, assetId), Value: ownerHoldingB},
		{Key: KeyForHolding(opecTreasury, assetId), Value: treasuryHoldingB},
		{Key: KeyForHoldingIndex(msg.OwnerAddress), Value: ownerHoldingIndexB},
		{Key: KeyForHoldingIndex(opecTreasury), Value: treasuryHoldingIndexB},
		{Key: KeyForAssetHolderIndex(assetId), Value: holderIndexB},
		{Key: KeyForAccount(msg.OwnerAddress), Value: ownerBytes},
		{Key: KeyForFeePool(c.Config.ChainId), Value: feePoolBytes},
	}
	if werr := c.stateWrite(sets, nil); werr != nil {
		return &PluginDeliverResponse{Error: werr}
	}
	log.Printf("TokenizeAsset SUCCESS: assetId=%d ownerFractions=%d treasuryFractions=%d", assetId, ownerFractions, treasuryFractions)
	return &PluginDeliverResponse{}
}

// ── buy_fraction ───────────────────────────────────────────────────────────────

func (c *Contract) CheckMessageBuyFraction(msg *MessageBuyFraction) *PluginCheckResponse {
	if len(msg.BuyerAddress) != 20 {
		return &PluginCheckResponse{Error: ErrInvalidAddress()}
	}
	if msg.Quantity == 0 {
		return &PluginCheckResponse{Error: ErrInvalidAmount()}
	}
	return &PluginCheckResponse{AuthorizedSigners: [][]byte{msg.BuyerAddress}}
}

func (c *Contract) DeliverMessageBuyFraction(msg *MessageBuyFraction, fee uint64) *PluginDeliverResponse {
	log.Printf("DeliverMessageBuyFraction: buyer=%x assetId=%d qty=%d", msg.BuyerAddress, msg.AssetId, msg.Quantity)

	buyerQId, assetQId, feePoolQId, holderIndexQId := rand.Uint64(), rand.Uint64(), rand.Uint64(), rand.Uint64()
	resp, err := c.plugin.StateRead(c, &PluginStateReadRequest{
		Keys: []*PluginKeyRead{
			{QueryId: buyerQId, Key: KeyForAccount(msg.BuyerAddress)},
			{QueryId: assetQId, Key: KeyForAsset(msg.AssetId)},
			{QueryId: feePoolQId, Key: KeyForFeePool(c.Config.ChainId)},
			{QueryId: holderIndexQId, Key: KeyForAssetHolderIndex(msg.AssetId)},
		},
	})
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if resp.Error != nil {
		return &PluginDeliverResponse{Error: resp.Error}
	}

	buyer, feePool := new(Account), new(Pool)
	asset := new(Asset)
	holderIndex := new(AssetHolderIndex)
	var buyerBytes, feePoolBytes []byte

	for _, r := range resp.Results {
		if len(r.Entries) == 0 {
			continue
		}
		switch r.QueryId {
		case buyerQId:
			buyerBytes = r.Entries[0].Value
		case assetQId:
			if uerr := Unmarshal(r.Entries[0].Value, asset); uerr != nil {
				return &PluginDeliverResponse{Error: uerr}
			}
		case feePoolQId:
			feePoolBytes = r.Entries[0].Value
		case holderIndexQId:
			if uerr := Unmarshal(r.Entries[0].Value, holderIndex); uerr != nil {
				return &PluginDeliverResponse{Error: uerr}
			}
		}
	}

	if asset.Id == 0 {
		return &PluginDeliverResponse{Error: ErrInvalidAmount()} // asset not found
	}
	if uerr := Unmarshal(buyerBytes, buyer); uerr != nil {
		return &PluginDeliverResponse{Error: uerr}
	}
	if uerr := Unmarshal(feePoolBytes, feePool); uerr != nil {
		return &PluginDeliverResponse{Error: uerr}
	}

	// Read owner holding
	ownerHolding := new(Holding)
	ownerHoldingKey := KeyForHolding(asset.OwnerAddress, msg.AssetId)
	ownerHoldingBytes, rerr := c.stateRead1(ownerHoldingKey)
	if rerr != nil {
		return &PluginDeliverResponse{Error: rerr}
	}
	if uerr := Unmarshal(ownerHoldingBytes, ownerHolding); uerr != nil {
		return &PluginDeliverResponse{Error: uerr}
	}
	if ownerHolding.Quantity < msg.Quantity {
		return &PluginDeliverResponse{Error: ErrInsufficientFunds()}
	}

	purchaseCost := msg.Quantity * asset.FractionPrice
	pFee := platformFee(purchaseCost)
	totalDeduct := purchaseCost + fee
	if buyer.Amount < totalDeduct {
		return &PluginDeliverResponse{Error: ErrInsufficientFunds()}
	}

	buyer.Amount -= totalDeduct
	feePool.Amount += fee

	// Owner receives purchase minus platform fee
	ownerAccount := new(Account)
	ownerAccBytes, rerr := c.stateRead1(KeyForAccount(asset.OwnerAddress))
	if rerr != nil {
		return &PluginDeliverResponse{Error: rerr}
	}
	if uerr := Unmarshal(ownerAccBytes, ownerAccount); uerr != nil {
		return &PluginDeliverResponse{Error: uerr}
	}
	ownerAccount.Amount += purchaseCost - pFee

	// Treasury gets platform fee
	treasury := new(Account)
	treasuryBytes, rerr := c.stateRead1(KeyForAccount(opecTreasury))
	if rerr != nil {
		return &PluginDeliverResponse{Error: rerr}
	}
	if uerr := Unmarshal(treasuryBytes, treasury); uerr != nil {
		return &PluginDeliverResponse{Error: uerr}
	}
	treasury.Amount += pFee

	// Update holdings
	ownerHolding.Quantity -= msg.Quantity

	// Read buyer holding (may not exist yet)
	buyerHolding := new(Holding)
	buyerHoldingKey := KeyForHolding(msg.BuyerAddress, msg.AssetId)
	buyerHoldingBytes, rerr := c.stateRead1(buyerHoldingKey)
	if rerr != nil {
		return &PluginDeliverResponse{Error: rerr}
	}
	isNewHolder := buyerHoldingBytes == nil
	if uerr := Unmarshal(buyerHoldingBytes, buyerHolding); uerr != nil {
		return &PluginDeliverResponse{Error: uerr}
	}
	buyerHolding.HolderAddress = msg.BuyerAddress
	buyerHolding.AssetId = msg.AssetId
	buyerHolding.Quantity += msg.Quantity

	// Update AssetHolderIndex if buyer is new
	if isNewHolder {
		holderIndex.HolderAddresses = append(holderIndex.HolderAddresses, msg.BuyerAddress)
	}

	// Update buyer HoldingIndex
	buyerHoldingIndex := new(HoldingIndex)
	bHIBytes, rerr := c.stateRead1(KeyForHoldingIndex(msg.BuyerAddress))
	if rerr != nil {
		return &PluginDeliverResponse{Error: rerr}
	}
	if uerr := Unmarshal(bHIBytes, buyerHoldingIndex); uerr != nil {
		return &PluginDeliverResponse{Error: uerr}
	}
	if isNewHolder {
		buyerHoldingIndex.AssetIds = append(buyerHoldingIndex.AssetIds, msg.AssetId)
	}

	// Marshal everything
	buyerBytes, merr := mustMarshal(buyer)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	feePoolBytes, merr = mustMarshal(feePool)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	ownerAccBytes, merr = mustMarshal(ownerAccount)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	treasuryBytes, merr = mustMarshal(treasury)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	buyerHoldingB, merr := mustMarshal(buyerHolding)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	holderIndexB, merr := mustMarshal(holderIndex)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	buyerHIB, merr := mustMarshal(buyerHoldingIndex)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}

	sets := []*PluginSetOp{
		{Key: KeyForAccount(msg.BuyerAddress), Value: buyerBytes},
		{Key: KeyForFeePool(c.Config.ChainId), Value: feePoolBytes},
		{Key: KeyForAccount(asset.OwnerAddress), Value: ownerAccBytes},
		{Key: KeyForAccount(opecTreasury), Value: treasuryBytes},
		{Key: buyerHoldingKey, Value: buyerHoldingB},
		{Key: KeyForAssetHolderIndex(msg.AssetId), Value: holderIndexB},
		{Key: KeyForHoldingIndex(msg.BuyerAddress), Value: buyerHIB},
	}
	var deletes []*PluginDeleteOp
	if ownerHolding.Quantity == 0 {
		deletes = append(deletes, &PluginDeleteOp{Key: ownerHoldingKey})
	} else {
		ownerHoldingB, merr := mustMarshal(ownerHolding)
		if merr != nil {
			return &PluginDeliverResponse{Error: merr}
		}
		sets = append(sets, &PluginSetOp{Key: ownerHoldingKey, Value: ownerHoldingB})
	}

	if werr := c.stateWrite(sets, deletes); werr != nil {
		return &PluginDeliverResponse{Error: werr}
	}
	log.Printf("BuyFraction SUCCESS: assetId=%d qty=%d cost=%d platformFee=%d", msg.AssetId, msg.Quantity, purchaseCost, pFee)
	return &PluginDeliverResponse{}
}

// ── transfer_fraction ──────────────────────────────────────────────────────────

func (c *Contract) CheckMessageTransferFraction(msg *MessageTransferFraction) *PluginCheckResponse {
	if len(msg.FromAddress) != 20 {
		return &PluginCheckResponse{Error: ErrInvalidAddress()}
	}
	if len(msg.ToAddress) != 20 {
		return &PluginCheckResponse{Error: ErrInvalidAddress()}
	}
	if msg.Quantity == 0 {
		return &PluginCheckResponse{Error: ErrInvalidAmount()}
	}
	return &PluginCheckResponse{Recipient: msg.ToAddress, AuthorizedSigners: [][]byte{msg.FromAddress}}
}

func (c *Contract) DeliverMessageTransferFraction(msg *MessageTransferFraction, fee uint64) *PluginDeliverResponse {
	log.Printf("DeliverMessageTransferFraction: from=%x to=%x assetId=%d qty=%d", msg.FromAddress, msg.ToAddress, msg.AssetId, msg.Quantity)

	fromAccQId, feePoolQId, fromHQId, toHQId := rand.Uint64(), rand.Uint64(), rand.Uint64(), rand.Uint64()
	fromHoldingKey := KeyForHolding(msg.FromAddress, msg.AssetId)
	toHoldingKey := KeyForHolding(msg.ToAddress, msg.AssetId)

	resp, err := c.plugin.StateRead(c, &PluginStateReadRequest{
		Keys: []*PluginKeyRead{
			{QueryId: fromAccQId, Key: KeyForAccount(msg.FromAddress)},
			{QueryId: feePoolQId, Key: KeyForFeePool(c.Config.ChainId)},
			{QueryId: fromHQId, Key: fromHoldingKey},
			{QueryId: toHQId, Key: toHoldingKey},
		},
	})
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if resp.Error != nil {
		return &PluginDeliverResponse{Error: resp.Error}
	}

	fromAcc, feePool := new(Account), new(Pool)
	fromHolding, toHolding := new(Holding), new(Holding)
	isNewToHolder := true
	var fromAccBytes, feePoolBytes []byte

	for _, r := range resp.Results {
		if len(r.Entries) == 0 {
			continue
		}
		switch r.QueryId {
		case fromAccQId:
			fromAccBytes = r.Entries[0].Value
		case feePoolQId:
			feePoolBytes = r.Entries[0].Value
		case fromHQId:
			if uerr := Unmarshal(r.Entries[0].Value, fromHolding); uerr != nil {
				return &PluginDeliverResponse{Error: uerr}
			}
		case toHQId:
			if uerr := Unmarshal(r.Entries[0].Value, toHolding); uerr != nil {
				return &PluginDeliverResponse{Error: uerr}
			}
			isNewToHolder = false
		}
	}

	if uerr := Unmarshal(fromAccBytes, fromAcc); uerr != nil {
		return &PluginDeliverResponse{Error: uerr}
	}
	if uerr := Unmarshal(feePoolBytes, feePool); uerr != nil {
		return &PluginDeliverResponse{Error: uerr}
	}
	if fromAcc.Amount < fee {
		return &PluginDeliverResponse{Error: ErrInsufficientFunds()}
	}
	if fromHolding.Quantity < msg.Quantity {
		return &PluginDeliverResponse{Error: ErrInsufficientFunds()}
	}

	fromAcc.Amount -= fee
	feePool.Amount += fee
	fromHolding.Quantity -= msg.Quantity
	toHolding.HolderAddress = msg.ToAddress
	toHolding.AssetId = msg.AssetId
	toHolding.Quantity += msg.Quantity

	fromAccBytes, merr := mustMarshal(fromAcc)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	feePoolBytes, merr = mustMarshal(feePool)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	toHoldingB, merr := mustMarshal(toHolding)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}

	sets := []*PluginSetOp{
		{Key: KeyForAccount(msg.FromAddress), Value: fromAccBytes},
		{Key: KeyForFeePool(c.Config.ChainId), Value: feePoolBytes},
		{Key: toHoldingKey, Value: toHoldingB},
	}
	var deletes []*PluginDeleteOp

	if fromHolding.Quantity == 0 {
		deletes = append(deletes, &PluginDeleteOp{Key: fromHoldingKey})
	} else {
		fromHoldingB, merr := mustMarshal(fromHolding)
		if merr != nil {
			return &PluginDeliverResponse{Error: merr}
		}
		sets = append(sets, &PluginSetOp{Key: fromHoldingKey, Value: fromHoldingB})
	}

	// If recipient is new, update AssetHolderIndex and their HoldingIndex
	if isNewToHolder {
		holderIndex := new(AssetHolderIndex)
		hiBytes, rerr := c.stateRead1(KeyForAssetHolderIndex(msg.AssetId))
		if rerr != nil {
			return &PluginDeliverResponse{Error: rerr}
		}
		if uerr := Unmarshal(hiBytes, holderIndex); uerr != nil {
			return &PluginDeliverResponse{Error: uerr}
		}
		holderIndex.HolderAddresses = append(holderIndex.HolderAddresses, msg.ToAddress)
		hiB, merr := mustMarshal(holderIndex)
		if merr != nil {
			return &PluginDeliverResponse{Error: merr}
		}
		sets = append(sets, &PluginSetOp{Key: KeyForAssetHolderIndex(msg.AssetId), Value: hiB})

		toHI := new(HoldingIndex)
		toHIBytes, rerr := c.stateRead1(KeyForHoldingIndex(msg.ToAddress))
		if rerr != nil {
			return &PluginDeliverResponse{Error: rerr}
		}
		if uerr := Unmarshal(toHIBytes, toHI); uerr != nil {
			return &PluginDeliverResponse{Error: uerr}
		}
		toHI.AssetIds = append(toHI.AssetIds, msg.AssetId)
		toHIB, merr := mustMarshal(toHI)
		if merr != nil {
			return &PluginDeliverResponse{Error: merr}
		}
		sets = append(sets, &PluginSetOp{Key: KeyForHoldingIndex(msg.ToAddress), Value: toHIB})
	}

	if werr := c.stateWrite(sets, deletes); werr != nil {
		return &PluginDeliverResponse{Error: werr}
	}
	log.Printf("TransferFraction SUCCESS: from=%x to=%x assetId=%d qty=%d", msg.FromAddress, msg.ToAddress, msg.AssetId, msg.Quantity)
	return &PluginDeliverResponse{}
}

// ── distribute_yield ───────────────────────────────────────────────────────────

func (c *Contract) CheckMessageDistributeYield(msg *MessageDistributeYield) *PluginCheckResponse {
	if len(msg.OwnerAddress) != 20 {
		return &PluginCheckResponse{Error: ErrInvalidAddress()}
	}
	if msg.TotalYield == 0 {
		return &PluginCheckResponse{Error: ErrInvalidAmount()}
	}
	return &PluginCheckResponse{AuthorizedSigners: [][]byte{msg.OwnerAddress}}
}

func (c *Contract) DeliverMessageDistributeYield(msg *MessageDistributeYield, fee uint64) *PluginDeliverResponse {
	log.Printf("DeliverMessageDistributeYield: owner=%x assetId=%d yield=%d", msg.OwnerAddress, msg.AssetId, msg.TotalYield)

	ownerQId, assetQId, feePoolQId, hiQId := rand.Uint64(), rand.Uint64(), rand.Uint64(), rand.Uint64()
	resp, err := c.plugin.StateRead(c, &PluginStateReadRequest{
		Keys: []*PluginKeyRead{
			{QueryId: ownerQId, Key: KeyForAccount(msg.OwnerAddress)},
			{QueryId: assetQId, Key: KeyForAsset(msg.AssetId)},
			{QueryId: feePoolQId, Key: KeyForFeePool(c.Config.ChainId)},
			{QueryId: hiQId, Key: KeyForAssetHolderIndex(msg.AssetId)},
		},
	})
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if resp.Error != nil {
		return &PluginDeliverResponse{Error: resp.Error}
	}

	ownerAcc, feePool := new(Account), new(Pool)
	asset := new(Asset)
	holderIndex := new(AssetHolderIndex)
	var ownerBytes, feePoolBytes []byte

	for _, r := range resp.Results {
		if len(r.Entries) == 0 {
			continue
		}
		switch r.QueryId {
		case ownerQId:
			ownerBytes = r.Entries[0].Value
		case assetQId:
			if uerr := Unmarshal(r.Entries[0].Value, asset); uerr != nil {
				return &PluginDeliverResponse{Error: uerr}
			}
		case feePoolQId:
			feePoolBytes = r.Entries[0].Value
		case hiQId:
			if uerr := Unmarshal(r.Entries[0].Value, holderIndex); uerr != nil {
				return &PluginDeliverResponse{Error: uerr}
			}
		}
	}

	if asset.Id == 0 {
		return &PluginDeliverResponse{Error: ErrInvalidAmount()}
	}
	if !bytes.Equal(asset.OwnerAddress, msg.OwnerAddress) {
		return &PluginDeliverResponse{Error: ErrInvalidAddress()}
	}
	if uerr := Unmarshal(ownerBytes, ownerAcc); uerr != nil {
		return &PluginDeliverResponse{Error: uerr}
	}
	if uerr := Unmarshal(feePoolBytes, feePool); uerr != nil {
		return &PluginDeliverResponse{Error: uerr}
	}

	totalDeduct := msg.TotalYield + fee
	if ownerAcc.Amount < totalDeduct {
		return &PluginDeliverResponse{Error: ErrInsufficientFunds()}
	}
	ownerAcc.Amount -= totalDeduct
	feePool.Amount += fee

	// Batch-read all holder holdings
	type holderData struct {
		addr    []byte
		qId     uint64
		holding *Holding
		payout  uint64
	}
	hds := make([]holderData, len(holderIndex.HolderAddresses))
	holdingKeys := make([]*PluginKeyRead, len(holderIndex.HolderAddresses))
	for i, addr := range holderIndex.HolderAddresses {
		qId := rand.Uint64()
		hds[i] = holderData{addr: addr, qId: qId, holding: new(Holding)}
		holdingKeys[i] = &PluginKeyRead{QueryId: qId, Key: KeyForHolding(addr, msg.AssetId)}
	}

	hResp, herr := c.plugin.StateRead(c, &PluginStateReadRequest{Keys: holdingKeys})
	if herr != nil {
		return &PluginDeliverResponse{Error: herr}
	}
	if hResp.Error != nil {
		return &PluginDeliverResponse{Error: hResp.Error}
	}
	for _, r := range hResp.Results {
		if len(r.Entries) == 0 {
			continue
		}
		for i := range hds {
			if hds[i].qId == r.QueryId {
				if uerr := Unmarshal(r.Entries[0].Value, hds[i].holding); uerr != nil {
					return &PluginDeliverResponse{Error: uerr}
				}
			}
		}
	}

	// Compute proportional payouts
	totalDistributed := uint64(0)
	for i := range hds {
		if hds[i].holding.Quantity == 0 || asset.TotalFractions == 0 {
			continue
		}
		hds[i].payout = msg.TotalYield * hds[i].holding.Quantity / asset.TotalFractions
		totalDistributed += hds[i].payout
	}
	remainder := msg.TotalYield - totalDistributed
	ownerAcc.Amount += remainder // remainder stays with owner

	// Batch-read holder accounts
	type accData struct {
		addr    []byte
		qId     uint64
		account *Account
		key     []byte
		payout  uint64
	}
	var accs []accData
	var accKeys []*PluginKeyRead
	for _, hd := range hds {
		if hd.payout == 0 {
			continue
		}
		qId := rand.Uint64()
		key := KeyForAccount(hd.addr)
		accs = append(accs, accData{addr: hd.addr, qId: qId, account: new(Account), key: key, payout: hd.payout})
		accKeys = append(accKeys, &PluginKeyRead{QueryId: qId, Key: key})
	}

	if len(accKeys) > 0 {
		aResp, aerr := c.plugin.StateRead(c, &PluginStateReadRequest{Keys: accKeys})
		if aerr != nil {
			return &PluginDeliverResponse{Error: aerr}
		}
		if aResp.Error != nil {
			return &PluginDeliverResponse{Error: aResp.Error}
		}
		for _, r := range aResp.Results {
			if len(r.Entries) == 0 {
				continue
			}
			for i := range accs {
				if accs[i].qId == r.QueryId {
					if uerr := Unmarshal(r.Entries[0].Value, accs[i].account); uerr != nil {
						return &PluginDeliverResponse{Error: uerr}
					}
				}
			}
		}
	}

	for i := range accs {
		accs[i].account.Amount += accs[i].payout
	}

	// Build write set
	ownerBytes, merr := mustMarshal(ownerAcc)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	feePoolBytes, merr = mustMarshal(feePool)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	sets := []*PluginSetOp{
		{Key: KeyForAccount(msg.OwnerAddress), Value: ownerBytes},
		{Key: KeyForFeePool(c.Config.ChainId), Value: feePoolBytes},
	}
	for _, ad := range accs {
		b, merr := mustMarshal(ad.account)
		if merr != nil {
			return &PluginDeliverResponse{Error: merr}
		}
		sets = append(sets, &PluginSetOp{Key: ad.key, Value: b})
	}

	if werr := c.stateWrite(sets, nil); werr != nil {
		return &PluginDeliverResponse{Error: werr}
	}
	log.Printf("DistributeYield SUCCESS: assetId=%d yield=%d distributed=%d remainder=%d", msg.AssetId, msg.TotalYield, totalDistributed, remainder)
	return &PluginDeliverResponse{}
}

// ── cast_vote ──────────────────────────────────────────────────────────────────

func (c *Contract) CheckMessageCastVote(msg *MessageCastVote) *PluginCheckResponse {
	if len(msg.VoterAddress) != 20 {
		return &PluginCheckResponse{Error: ErrInvalidAddress()}
	}
	return &PluginCheckResponse{AuthorizedSigners: [][]byte{msg.VoterAddress}}
}

func (c *Contract) DeliverMessageCastVote(msg *MessageCastVote, fee uint64) *PluginDeliverResponse {
	log.Printf("DeliverMessageCastVote: voter=%x proposalId=%d approve=%v", msg.VoterAddress, msg.ProposalId, msg.Approve)

	voterQId, proposalQId, voteQId, hiQId, feePoolQId := rand.Uint64(), rand.Uint64(), rand.Uint64(), rand.Uint64(), rand.Uint64()
	resp, err := c.plugin.StateRead(c, &PluginStateReadRequest{
		Keys: []*PluginKeyRead{
			{QueryId: voterQId, Key: KeyForAccount(msg.VoterAddress)},
			{QueryId: proposalQId, Key: KeyForProposal(msg.ProposalId)},
			{QueryId: voteQId, Key: KeyForVoteRecord(msg.VoterAddress, msg.ProposalId)},
			{QueryId: hiQId, Key: KeyForHoldingIndex(msg.VoterAddress)},
			{QueryId: feePoolQId, Key: KeyForFeePool(c.Config.ChainId)},
		},
	})
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if resp.Error != nil {
		return &PluginDeliverResponse{Error: resp.Error}
	}

	voterAcc, feePool := new(Account), new(Pool)
	proposal := new(Proposal)
	voteRecord := new(VoteRecord)
	holdingIndex := new(HoldingIndex)
	var voterBytes, feePoolBytes []byte

	for _, r := range resp.Results {
		if len(r.Entries) == 0 {
			continue
		}
		switch r.QueryId {
		case voterQId:
			voterBytes = r.Entries[0].Value
		case proposalQId:
			if uerr := Unmarshal(r.Entries[0].Value, proposal); uerr != nil {
				return &PluginDeliverResponse{Error: uerr}
			}
		case voteQId:
			if uerr := Unmarshal(r.Entries[0].Value, voteRecord); uerr != nil {
				return &PluginDeliverResponse{Error: uerr}
			}
		case hiQId:
			if uerr := Unmarshal(r.Entries[0].Value, holdingIndex); uerr != nil {
				return &PluginDeliverResponse{Error: uerr}
			}
		case feePoolQId:
			feePoolBytes = r.Entries[0].Value
		}
	}

	if proposal.Id == 0 {
		return &PluginDeliverResponse{Error: ErrInvalidAmount()} // proposal not found
	}
	if voteRecord.Voted {
		return &PluginDeliverResponse{Error: ErrInvalidAmount()} // already voted
	}
	if uerr := Unmarshal(voterBytes, voterAcc); uerr != nil {
		return &PluginDeliverResponse{Error: uerr}
	}
	if uerr := Unmarshal(feePoolBytes, feePool); uerr != nil {
		return &PluginDeliverResponse{Error: uerr}
	}
	if voterAcc.Amount < fee {
		return &PluginDeliverResponse{Error: ErrInsufficientFunds()}
	}

	// Vote weight = sum of fractions held across all assets
	voteWeight := uint64(0)
	if len(holdingIndex.AssetIds) > 0 {
		hKeys := make([]*PluginKeyRead, len(holdingIndex.AssetIds))
		qIds := make([]uint64, len(holdingIndex.AssetIds))
		for i, assetId := range holdingIndex.AssetIds {
			qId := rand.Uint64()
			qIds[i] = qId
			hKeys[i] = &PluginKeyRead{QueryId: qId, Key: KeyForHolding(msg.VoterAddress, assetId)}
		}
		hResp, herr := c.plugin.StateRead(c, &PluginStateReadRequest{Keys: hKeys})
		if herr != nil {
			return &PluginDeliverResponse{Error: herr}
		}
		if hResp.Error != nil {
			return &PluginDeliverResponse{Error: hResp.Error}
		}
		for _, r := range hResp.Results {
			if len(r.Entries) == 0 {
				continue
			}
			h := new(Holding)
			if uerr := Unmarshal(r.Entries[0].Value, h); uerr != nil {
				return &PluginDeliverResponse{Error: uerr}
			}
			voteWeight += h.Quantity
		}
	}
	if voteWeight == 0 {
		return &PluginDeliverResponse{Error: ErrInvalidAmount()} // no fractions = no vote
	}

	voterAcc.Amount -= fee
	feePool.Amount += fee
	if msg.Approve {
		proposal.YesVotes += voteWeight
	} else {
		proposal.NoVotes += voteWeight
	}
	voteRecord.Voted = true

	voterBytes, merr := mustMarshal(voterAcc)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	feePoolBytes, merr = mustMarshal(feePool)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	proposalB, merr := mustMarshal(proposal)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}
	voteRecordB, merr := mustMarshal(voteRecord)
	if merr != nil {
		return &PluginDeliverResponse{Error: merr}
	}

	if werr := c.stateWrite([]*PluginSetOp{
		{Key: KeyForAccount(msg.VoterAddress), Value: voterBytes},
		{Key: KeyForFeePool(c.Config.ChainId), Value: feePoolBytes},
		{Key: KeyForProposal(msg.ProposalId), Value: proposalB},
		{Key: KeyForVoteRecord(msg.VoterAddress, msg.ProposalId), Value: voteRecordB},
	}, nil); werr != nil {
		return &PluginDeliverResponse{Error: werr}
	}
	log.Printf("CastVote SUCCESS: voter=%x proposalId=%d approve=%v weight=%d", msg.VoterAddress, msg.ProposalId, msg.Approve, voteWeight)
	return &PluginDeliverResponse{}
}

// msgFromAny() directly unmarshals plugin message types by TypeUrl
func msgFromAny(a *anypb.Any) (proto.Message, *PluginError) {
if a == nil {
return nil, ErrFromAny(fmt.Errorf("nil any"))
}
var msg proto.Message
switch a.TypeUrl {
case "type.googleapis.com/types.MessageSend":
msg = new(MessageSend)
case "type.googleapis.com/types.MessageTokenizeAsset":
msg = new(MessageTokenizeAsset)
case "type.googleapis.com/types.MessageBuyFraction":
msg = new(MessageBuyFraction)
case "type.googleapis.com/types.MessageTransferFraction":
msg = new(MessageTransferFraction)
case "type.googleapis.com/types.MessageDistributeYield":
msg = new(MessageDistributeYield)
case "type.googleapis.com/types.MessageCastVote":
msg = new(MessageCastVote)
default:
return nil, ErrFromAny(fmt.Errorf("unknown type url: %s", a.TypeUrl))
}
if err := proto.Unmarshal(a.Value, msg); err != nil {
return nil, ErrFromAny(err)
}
return msg, nil
}
