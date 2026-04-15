// ============================================================
// PROPEX — Fractional RWA Ownership Plugin for Canopy Network
// plugin/propex.go · Go FSM Template · $PPX
// Built with Canopy Templates + Claude AI
// ============================================================

package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ─── CONSTANTS ───────────────────────────────────────────────

const (
	PluginID           = "propex-rwa-v1"
	PlatformFeeBPS     = 150    // 1.5% on trades
	MinFractions       = 100
	MaxFractions       = 1_000_000
	MinFractionPrice   = 100_000 // 0.1 CNPY in nCNPY
	GovernanceDuration = 2016   // ~1 week in blocks
	QuorumBPS          = 1000   // 10% of fractions must vote
)

// ─── STATE DB INTERFACE ──────────────────────────────────────

type StateDB interface {
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
	Delete(key string) error
}

// ─── RPC INTERFACE ───────────────────────────────────────────

type RPC interface {
	GetBlockHeight() (uint64, error)
	TransferTokens(address string, amount uint64) error
	EmitEvent(name string, data map[string]interface{})
}

// ─── TYPES ───────────────────────────────────────────────────

type AssetStatus string

const (
	StatusPendingReview AssetStatus = "pending_review"
	StatusActive        AssetStatus = "active"
	StatusFullyFunded   AssetStatus = "fully_funded"
	StatusYielding      AssetStatus = "yielding"
	StatusGovernance    AssetStatus = "governance"
	StatusDelisted      AssetStatus = "delisted"
)

type ProposalStatus string

const (
	ProposalActive   ProposalStatus = "active"
	ProposalPassed   ProposalStatus = "passed"
	ProposalRejected ProposalStatus = "rejected"
	ProposalExecuted ProposalStatus = "executed"
)

type Asset struct {
	ID                    string             `json:"id"`
	Owner                 string             `json:"owner"`
	Title                 string             `json:"title"`
	Description           string             `json:"description"`
	AssetClass            string             `json:"assetClass"`
	Location              string             `json:"location"`
	TotalValue            uint64             `json:"totalValue"`      // USD cents
	TotalFractions        uint64             `json:"totalFractions"`
	FractionPrice         uint64             `json:"fractionPrice"`   // nCNPY
	SoldFractions         uint64             `json:"soldFractions"`
	YieldRate             uint64             `json:"yieldRate"`       // annual % × 100
	YieldPeriodBlocks     uint64             `json:"yieldPeriodBlocks"`
	LastYieldBlock        uint64             `json:"lastYieldBlock"`
	TotalYieldDistributed uint64             `json:"totalYieldDistributed"`
	MetadataURI           string             `json:"metadataUri"`
	Status                AssetStatus        `json:"status"`
	Holders               map[string]uint64  `json:"holders"`
	ListedAtBlock         uint64             `json:"listedAtBlock"`
	ListedAt              int64              `json:"listedAt"`
}

type HolderPortfolio struct {
	Address           string             `json:"address"`
	Holdings          map[string]uint64  `json:"holdings"`  // assetId → fraction count
	TotalYieldClaimed uint64             `json:"totalYieldClaimed"`
	UnclaimedYield    uint64             `json:"unclaimedYield"`
	LastClaimBlock    uint64             `json:"lastClaimBlock"`
	TradeHistory      []Trade            `json:"tradeHistory"`
}

type Trade struct {
	ID               string `json:"id"`
	AssetID          string `json:"assetId"`
	Seller           string `json:"seller"`
	Buyer            string `json:"buyer"`
	Fractions        uint64 `json:"fractions"`
	PricePerFraction uint64 `json:"pricePerFraction"`
	TotalPrice       uint64 `json:"totalPrice"`
	BlockHeight      uint64 `json:"blockHeight"`
	Timestamp        int64  `json:"timestamp"`
}

type Listing struct {
	ID                 string `json:"id"`
	AssetID            string `json:"assetId"`
	Seller             string `json:"seller"`
	Fractions          uint64 `json:"fractions"`
	AskPricePerFraction uint64 `json:"askPricePerFraction"`
	CreatedAt          int64  `json:"createdAt"`
	BlockHeight        uint64 `json:"blockHeight"`
	Active             bool   `json:"active"`
}

type GovernanceProposal struct {
	ID             string            `json:"id"`
	AssetID        string            `json:"assetId"`
	Proposer       string            `json:"proposer"`
	Title          string            `json:"title"`
	Description    string            `json:"description"`
	VotesFor       uint64            `json:"votesFor"`
	VotesAgainst   uint64            `json:"votesAgainst"`
	Voters         map[string]bool   `json:"voters"`
	Status         ProposalStatus    `json:"status"`
	CreatedAtBlock uint64            `json:"createdAtBlock"`
	ExpiresAtBlock uint64            `json:"expiresAtBlock"`
}

// ─── KEY HELPERS ─────────────────────────────────────────────

func assetKey(id string) string     { return "asset:" + id }
func portfolioKey(addr string) string { return "portfolio:" + addr }
func listingKey(id string) string   { return "listing:" + id }
func proposalKey(id string) string  { return "proposal:" + id }

const (
	registryKey = "registry:assets"
	marketKey   = "registry:listings"
	treasuryKey = "treasury:ppx"
)

// ─── STATE HELPERS ───────────────────────────────────────────

func getJSON(db StateDB, key string, out interface{}) error {
	raw, err := db.Get(key)
	if err != nil || raw == nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func setJSON(db StateDB, key string, val interface{}) error {
	raw, err := json.Marshal(val)
	if err != nil {
		return err
	}
	return db.Set(key, raw)
}

func getRegistry(db StateDB) ([]string, error) {
	var reg []string
	_ = getJSON(db, registryKey, &reg)
	return reg, nil
}

func getMarket(db StateDB) ([]string, error) {
	var market []string
	_ = getJSON(db, marketKey, &market)
	return market, nil
}

func getTreasury(db StateDB) uint64 {
	var t uint64
	_ = getJSON(db, treasuryKey, &t)
	return t
}

func addToTreasury(db StateDB, amount uint64) error {
	t := getTreasury(db)
	return setJSON(db, treasuryKey, t+amount)
}

func requireAsset(db StateDB, assetID string) (*Asset, error) {
	var a Asset
	err := getJSON(db, assetKey(assetID), &a)
	if err != nil || a.ID == "" {
		return nil, fmt.Errorf("asset %s not found", assetID)
	}
	return &a, nil
}

func getOrCreatePortfolio(db StateDB, addr string) *HolderPortfolio {
	var p HolderPortfolio
	_ = getJSON(db, portfolioKey(addr), &p)
	if p.Address == "" {
		p = HolderPortfolio{
			Address:      addr,
			Holdings:     make(map[string]uint64),
			TradeHistory: []Trade{},
		}
	}
	if p.Holdings == nil {
		p.Holdings = make(map[string]uint64)
	}
	return &p
}

func requirePortfolio(db StateDB, addr string) (*HolderPortfolio, error) {
	var p HolderPortfolio
	_ = getJSON(db, portfolioKey(addr), &p)
	if p.Address == "" {
		return nil, fmt.Errorf("no portfolio for %s — buy fractions first", addr)
	}
	if p.Holdings == nil {
		p.Holdings = make(map[string]uint64)
	}
	return &p, nil
}

// ═══════════════════════════════════════════════════════════
// ASSET LIFECYCLE
// ═══════════════════════════════════════════════════════════

// ListAsset — owner lists a real-world asset for fractional tokenization
func ListAsset(db StateDB, rpc RPC, sender string, params map[string]interface{}) (string, error) {
	totalFractions := uint64(params["totalFractions"].(float64))
	fractionPrice  := uint64(params["fractionPrice"].(float64))

	if totalFractions < MinFractions || totalFractions > MaxFractions {
		return "", fmt.Errorf("fractions must be between %d and %d", MinFractions, MaxFractions)
	}
	if fractionPrice < MinFractionPrice {
		return "", fmt.Errorf("minimum fraction price is %d nCNPY", MinFractionPrice)
	}

	block, err := rpc.GetBlockHeight()
	if err != nil {
		return "", err
	}

	id := fmt.Sprintf("ppx-%s-%d", sender[:8], block)

	asset := Asset{
		ID:                id,
		Owner:             sender,
		Title:             params["title"].(string),
		Description:       params["description"].(string),
		AssetClass:        params["assetClass"].(string),
		Location:          params["location"].(string),
		TotalValue:        uint64(params["totalValue"].(float64)),
		TotalFractions:    totalFractions,
		FractionPrice:     fractionPrice,
		SoldFractions:     0,
		YieldRate:         uint64(params["yieldRate"].(float64)),
		YieldPeriodBlocks: uint64(params["yieldPeriodBlocks"].(float64)),
		LastYieldBlock:    block,
		MetadataURI:       params["metadataUri"].(string),
		Status:            StatusPendingReview,
		Holders:           make(map[string]uint64),
		ListedAtBlock:     block,
		ListedAt:          time.Now().UnixMilli(),
	}

	if err := setJSON(db, assetKey(id), asset); err != nil {
		return "", err
	}

	reg, _ := getRegistry(db)
	reg = append(reg, id)
	if err := setJSON(db, registryKey, reg); err != nil {
		return "", err
	}

	rpc.EmitEvent("AssetListed", map[string]interface{}{
		"id": id, "owner": sender, "assetClass": asset.AssetClass, "block": block,
	})
	return id, nil
}

// ApproveAsset — platform verifier activates a pending asset
func ApproveAsset(db StateDB, rpc RPC, sender string, params map[string]interface{}) error {
	assetID := params["assetId"].(string)
	asset, err := requireAsset(db, assetID)
	if err != nil {
		return err
	}
	if asset.Status != StatusPendingReview {
		return errors.New("asset is not pending review")
	}
	asset.Status = StatusActive
	if err := setJSON(db, assetKey(assetID), asset); err != nil {
		return err
	}
	rpc.EmitEvent("AssetApproved", map[string]interface{}{"assetId": assetID, "approver": sender})
	return nil
}

// ═══════════════════════════════════════════════════════════
// FRACTIONAL BUYING
// ═══════════════════════════════════════════════════════════

// BuyFractions — buy fractions directly from the asset pool
func BuyFractions(db StateDB, rpc RPC, sender string, params map[string]interface{}) error {
	assetID       := params["assetId"].(string)
	fractionCount := uint64(params["fractionCount"].(float64))
	attachedFee   := uint64(params["attachedFee"].(float64))

	asset, err := requireAsset(db, assetID)
	if err != nil {
		return err
	}
	if asset.Status != StatusActive {
		return errors.New("asset is not available for purchase")
	}

	available := asset.TotalFractions - asset.SoldFractions
	if fractionCount > available {
		return fmt.Errorf("only %d fractions available", available)
	}
	if fractionCount == 0 {
		return errors.New("must buy at least 1 fraction")
	}

	totalCost := fractionCount * asset.FractionPrice
	if attachedFee < totalCost {
		return fmt.Errorf("insufficient payment: required %d nCNPY", totalCost)
	}

	platformFee := (totalCost * PlatformFeeBPS) / 10000
	if err := addToTreasury(db, platformFee); err != nil {
		return err
	}

	asset.SoldFractions += fractionCount
	asset.Holders[sender] += fractionCount
	if asset.SoldFractions == asset.TotalFractions {
		asset.Status = StatusFullyFunded
	}
	if err := setJSON(db, assetKey(assetID), asset); err != nil {
		return err
	}

	portfolio := getOrCreatePortfolio(db, sender)
	portfolio.Holdings[assetID] += fractionCount
	if err := setJSON(db, portfolioKey(sender), portfolio); err != nil {
		return err
	}

	block, _ := rpc.GetBlockHeight()
	rpc.EmitEvent("FractionsPurchased", map[string]interface{}{
		"assetId": assetID, "buyer": sender,
		"fractionCount": fractionCount, "totalCost": totalCost, "block": block,
	})
	return nil
}

// ═══════════════════════════════════════════════════════════
// SECONDARY MARKET
// ═══════════════════════════════════════════════════════════

// CreateListing — holder lists fractions on secondary market
func CreateListing(db StateDB, rpc RPC, sender string, params map[string]interface{}) (string, error) {
	assetID              := params["assetId"].(string)
	fractions            := uint64(params["fractions"].(float64))
	askPricePerFraction  := uint64(params["askPricePerFraction"].(float64))

	portfolio, err := requirePortfolio(db, sender)
	if err != nil {
		return "", err
	}

	held := portfolio.Holdings[assetID]
	if fractions == 0 || fractions > held {
		return "", fmt.Errorf("you only hold %d fractions of this asset", held)
	}

	block, _ := rpc.GetBlockHeight()
	id := fmt.Sprintf("lst-%s-%d", sender[:8], block)

	listing := Listing{
		ID:                  id,
		AssetID:             assetID,
		Seller:              sender,
		Fractions:           fractions,
		AskPricePerFraction: askPricePerFraction,
		CreatedAt:           time.Now().UnixMilli(),
		BlockHeight:         block,
		Active:              true,
	}

	if err := setJSON(db, listingKey(id), listing); err != nil {
		return "", err
	}

	market, _ := getMarket(db)
	market = append(market, id)
	if err := setJSON(db, marketKey, market); err != nil {
		return "", err
	}

	rpc.EmitEvent("ListingCreated", map[string]interface{}{
		"listingId": id, "assetId": assetID, "seller": sender,
	})
	return id, nil
}

// FillListing — buyer purchases fractions from secondary market
func FillListing(db StateDB, rpc RPC, sender string, params map[string]interface{}) error {
	listingID     := params["listingId"].(string)
	fractionsToBuy := uint64(params["fractionsToBuy"].(float64))
	attachedFee   := uint64(params["attachedFee"].(float64))

	var listing Listing
	if err := getJSON(db, listingKey(listingID), &listing); err != nil || !listing.Active {
		return errors.New("listing not found or inactive")
	}
	if fractionsToBuy > listing.Fractions {
		return fmt.Errorf("listing only has %d fractions", listing.Fractions)
	}

	totalCost := fractionsToBuy * listing.AskPricePerFraction
	if attachedFee < totalCost {
		return fmt.Errorf("insufficient funds: required %d nCNPY", totalCost)
	}

	platformFee := (totalCost * PlatformFeeBPS) / 10000
	if err := addToTreasury(db, platformFee); err != nil {
		return err
	}

	// Transfer fractions: seller → buyer
	sellerPortfolio, err := requirePortfolio(db, listing.Seller)
	if err != nil {
		return err
	}
	sellerPortfolio.Holdings[listing.AssetID] -= fractionsToBuy
	if sellerPortfolio.Holdings[listing.AssetID] == 0 {
		delete(sellerPortfolio.Holdings, listing.AssetID)
	}

	buyerPortfolio := getOrCreatePortfolio(db, sender)
	buyerPortfolio.Holdings[listing.AssetID] += fractionsToBuy

	// Update asset holder map
	asset, err := requireAsset(db, listing.AssetID)
	if err != nil {
		return err
	}
	if asset.Holders[listing.Seller] <= fractionsToBuy {
		delete(asset.Holders, listing.Seller)
	} else {
		asset.Holders[listing.Seller] -= fractionsToBuy
	}
	asset.Holders[sender] += fractionsToBuy
	if err := setJSON(db, assetKey(listing.AssetID), asset); err != nil {
		return err
	}

	// Record trade
	block, _ := rpc.GetBlockHeight()
	trade := Trade{
		ID:               fmt.Sprintf("trd-%d-%d", block, time.Now().UnixMilli()),
		AssetID:          listing.AssetID,
		Seller:           listing.Seller,
		Buyer:            sender,
		Fractions:        fractionsToBuy,
		PricePerFraction: listing.AskPricePerFraction,
		TotalPrice:       totalCost,
		BlockHeight:      block,
		Timestamp:        time.Now().UnixMilli(),
	}
	buyerPortfolio.TradeHistory = append(buyerPortfolio.TradeHistory, trade)
	sellerPortfolio.TradeHistory = append(sellerPortfolio.TradeHistory, trade)

	if err := setJSON(db, portfolioKey(listing.Seller), sellerPortfolio); err != nil {
		return err
	}
	if err := setJSON(db, portfolioKey(sender), buyerPortfolio); err != nil {
		return err
	}

	listing.Fractions -= fractionsToBuy
	if listing.Fractions == 0 {
		listing.Active = false
	}
	if err := setJSON(db, listingKey(listingID), listing); err != nil {
		return err
	}

	rpc.EmitEvent("ListingFilled", map[string]interface{}{
		"listingId": listingID, "buyer": sender,
		"seller": listing.Seller, "fractions": fractionsToBuy, "totalCost": totalCost,
	})
	return nil
}

// CancelListing — seller cancels their active listing
func CancelListing(db StateDB, rpc RPC, sender string, params map[string]interface{}) error {
	listingID := params["listingId"].(string)
	var listing Listing
	if err := getJSON(db, listingKey(listingID), &listing); err != nil || listing.ID == "" {
		return errors.New("listing not found")
	}
	if listing.Seller != sender {
		return errors.New("not your listing")
	}
	listing.Active = false
	if err := setJSON(db, listingKey(listingID), listing); err != nil {
		return err
	}
	rpc.EmitEvent("ListingCancelled", map[string]interface{}{"listingId": listingID, "seller": sender})
	return nil
}

// ═══════════════════════════════════════════════════════════
// YIELD DISTRIBUTION
// ═══════════════════════════════════════════════════════════

// DistributeYield — owner deposits yield distributed proportionally to all holders
func DistributeYield(db StateDB, rpc RPC, sender string, params map[string]interface{}) error {
	assetID     := params["assetId"].(string)
	yieldAmount := uint64(params["yieldAmount"].(float64))

	asset, err := requireAsset(db, assetID)
	if err != nil {
		return err
	}
	if asset.Owner != sender {
		return errors.New("only asset owner can distribute yield")
	}
	if asset.SoldFractions == 0 {
		return errors.New("no fraction holders to distribute to")
	}

	yieldPerFraction := yieldAmount / asset.SoldFractions

	for holderAddr, fractionCount := range asset.Holders {
		if fractionCount == 0 {
			continue
		}
		holderYield := yieldPerFraction * fractionCount
		p := getOrCreatePortfolio(db, holderAddr)
		p.UnclaimedYield += holderYield
		if err := setJSON(db, portfolioKey(holderAddr), p); err != nil {
			return err
		}
	}

	block, _ := rpc.GetBlockHeight()
	asset.LastYieldBlock = block
	asset.TotalYieldDistributed += yieldAmount
	asset.Status = StatusYielding
	if err := setJSON(db, assetKey(assetID), asset); err != nil {
		return err
	}

	rpc.EmitEvent("YieldDistributed", map[string]interface{}{
		"assetId": assetID, "totalAmount": yieldAmount,
		"yieldPerFraction": yieldPerFraction,
		"holderCount": len(asset.Holders), "block": block,
	})
	return nil
}

// ClaimYield — holder claims their accumulated yield
func ClaimYield(db StateDB, rpc RPC, sender string) (uint64, error) {
	portfolio, err := requirePortfolio(db, sender)
	if err != nil {
		return 0, err
	}
	if portfolio.UnclaimedYield == 0 {
		return 0, errors.New("no yield to claim")
	}

	amount := portfolio.UnclaimedYield
	portfolio.UnclaimedYield = 0
	portfolio.TotalYieldClaimed += amount

	block, _ := rpc.GetBlockHeight()
	portfolio.LastClaimBlock = block

	if err := setJSON(db, portfolioKey(sender), portfolio); err != nil {
		return 0, err
	}
	if err := rpc.TransferTokens(sender, amount); err != nil {
		return 0, err
	}

	rpc.EmitEvent("YieldClaimed", map[string]interface{}{"holder": sender, "amount": amount})
	return amount, nil
}

// ═══════════════════════════════════════════════════════════
// DAO GOVERNANCE
// ═══════════════════════════════════════════════════════════

// CreateProposal — any fraction holder can propose a governance action
func CreateProposal(db StateDB, rpc RPC, sender string, params map[string]interface{}) (string, error) {
	assetID     := params["assetId"].(string)
	title       := params["title"].(string)
	description := params["description"].(string)

	_, err := requireAsset(db, assetID)
	if err != nil {
		return "", err
	}

	portfolio, err := requirePortfolio(db, sender)
	if err != nil {
		return "", err
	}
	if portfolio.Holdings[assetID] == 0 {
		return "", errors.New("must hold fractions to create proposals")
	}

	block, _ := rpc.GetBlockHeight()
	id := fmt.Sprintf("prop-%s-%d", assetID, block)

	proposal := GovernanceProposal{
		ID:             id,
		AssetID:        assetID,
		Proposer:       sender,
		Title:          title,
		Description:    description,
		VotesFor:       0,
		VotesAgainst:   0,
		Voters:         make(map[string]bool),
		Status:         ProposalActive,
		CreatedAtBlock: block,
		ExpiresAtBlock: block + GovernanceDuration,
	}

	if err := setJSON(db, proposalKey(id), proposal); err != nil {
		return "", err
	}

	asset, _ := requireAsset(db, assetID)
	asset.Status = StatusGovernance
	if err := setJSON(db, assetKey(assetID), asset); err != nil {
		return "", err
	}

	rpc.EmitEvent("ProposalCreated", map[string]interface{}{
		"proposalId": id, "assetId": assetID, "proposer": sender,
	})
	return id, nil
}

// CastVote — fraction holders vote, weight = fraction count held
func CastVote(db StateDB, rpc RPC, sender string, params map[string]interface{}) error {
	proposalID := params["proposalId"].(string)
	support    := params["support"].(bool)

	var proposal GovernanceProposal
	if err := getJSON(db, proposalKey(proposalID), &proposal); err != nil || proposal.ID == "" {
		return errors.New("proposal not found")
	}
	if proposal.Status != ProposalActive {
		return errors.New("proposal is not active")
	}
	if proposal.Voters[sender] {
		return errors.New("already voted")
	}

	block, _ := rpc.GetBlockHeight()
	if block > proposal.ExpiresAtBlock {
		return errors.New("proposal has expired")
	}

	portfolio, err := requirePortfolio(db, sender)
	if err != nil {
		return err
	}
	voteWeight := portfolio.Holdings[proposal.AssetID]
	if voteWeight == 0 {
		return errors.New("no fractions held for this asset")
	}

	proposal.Voters[sender] = true
	if support {
		proposal.VotesFor += voteWeight
	} else {
		proposal.VotesAgainst += voteWeight
	}

	// Check quorum
	asset, _ := requireAsset(db, proposal.AssetID)
	totalVotes := proposal.VotesFor + proposal.VotesAgainst
	quorumRequired := (asset.SoldFractions * QuorumBPS) / 10000

	if totalVotes >= quorumRequired {
		if proposal.VotesFor > proposal.VotesAgainst {
			proposal.Status = ProposalPassed
		} else {
			proposal.Status = ProposalRejected
		}
		rpc.EmitEvent("ProposalResolved", map[string]interface{}{
			"proposalId": proposalID, "status": proposal.Status,
		})
	}

	if err := setJSON(db, proposalKey(proposalID), proposal); err != nil {
		return err
	}

	rpc.EmitEvent("VoteCast", map[string]interface{}{
		"proposalId": proposalID, "voter": sender,
		"support": support, "weight": voteWeight,
	})
	return nil
}

// ═══════════════════════════════════════════════════════════
// QUERIES
// ═══════════════════════════════════════════════════════════

func GetAsset(db StateDB, assetID string) (*Asset, error) {
	var a Asset
	err := getJSON(db, assetKey(assetID), &a)
	if err != nil || a.ID == "" {
		return nil, err
	}
	return &a, nil
}

func GetRegistry(db StateDB) ([]string, error) {
	return getRegistry(db)
}

func GetPortfolio(db StateDB, address string) (*HolderPortfolio, error) {
	var p HolderPortfolio
	err := getJSON(db, portfolioKey(address), &p)
	if err != nil || p.Address == "" {
		return nil, err
	}
	return &p, nil
}

func GetMarket(db StateDB) ([]string, error) {
	return getMarket(db)
}

func GetListing(db StateDB, listingID string) (*Listing, error) {
	var l Listing
	err := getJSON(db, listingKey(listingID), &l)
	if err != nil || l.ID == "" {
		return nil, err
	}
	return &l, nil
}

func GetProposal(db StateDB, proposalID string) (*GovernanceProposal, error) {
	var p GovernanceProposal
	err := getJSON(db, proposalKey(proposalID), &p)
	if err != nil || p.ID == "" {
		return nil, err
	}
	return &p, nil
}

func GetTreasury(db StateDB) uint64 {
	return getTreasury(db)
}

