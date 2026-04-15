// ============================================================
// PROPEX — Canopy RPC Client
// canopy-client.ts · $PPX
// ============================================================

const CANOPY_RPC = "https://rpc.canopynetwork.xyz";
const PLUGIN_ID  = "propex-rwa-v1";

// ─── TYPES ──────────────────────────────────────────────────

export interface RPCResult<T = unknown> {
  ok: boolean;
  data?: T;
  error?: string;
  txHash?: string;
  blockHeight?: number;
}

export type AssetClass =
  | "real_estate_residential"
  | "real_estate_commercial"
  | "art"
  | "commodities"
  | "equity"
  | "infrastructure"
  | "collectibles"
  | "other";

// ─── CORE RPC ───────────────────────────────────────────────

async function rpcPost<T = unknown>(
  method: string,
  params: Record<string, unknown>
): Promise<RPCResult<T>> {
  try {
    const res = await fetch(`${CANOPY_RPC}/v1/plugin/${PLUGIN_ID}/${method}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(params),
    });
    const json = await res.json();
    if (!res.ok) return { ok: false, error: json.message ?? "RPC error" };
    return { ok: true, data: json.result, txHash: json.txHash, blockHeight: json.blockHeight };
  } catch (e: unknown) {
    return { ok: false, error: e instanceof Error ? e.message : "Network error" };
  }
}

async function rpcGet<T = unknown>(
  method: string,
  params: Record<string, unknown> = {}
): Promise<RPCResult<T>> {
  try {
    const qs = new URLSearchParams(
      Object.entries(params).map(([k, v]) => [k, String(v)])
    ).toString();
    const res = await fetch(
      `${CANOPY_RPC}/v1/plugin/${PLUGIN_ID}/${method}${qs ? "?" + qs : ""}`
    );
    const json = await res.json();
    if (!res.ok) return { ok: false, error: json.message ?? "RPC error" };
    return { ok: true, data: json.result, blockHeight: json.blockHeight };
  } catch (e: unknown) {
    return { ok: false, error: e instanceof Error ? e.message : "Network error" };
  }
}

// ─── CHAIN ──────────────────────────────────────────────────

export async function getBlockHeight(): Promise<RPCResult<number>> {
  return rpcGet<number>("block_height");
}

export async function getBalance(address: string): Promise<RPCResult<number>> {
  return rpcGet<number>("balance", { address });
}

// ─── ASSET LIFECYCLE ────────────────────────────────────────

export async function listAsset(params: {
  sender: string;
  title: string;
  description: string;
  assetClass: AssetClass;
  location: string;
  totalValue: number;
  totalFractions: number;
  fractionPrice: number;
  yieldRate: number;
  yieldPeriodBlocks: number;
  metadataUri: string;
  signature: string;
}): Promise<RPCResult<string>> {
  return rpcPost<string>("ListAsset", params);
}

export async function approveAsset(params: {
  sender: string;
  assetId: string;
  signature: string;
}): Promise<RPCResult> {
  return rpcPost("ApproveAsset", params);
}

// ─── FRACTIONAL BUYING ──────────────────────────────────────

export async function buyFractions(params: {
  sender: string;
  assetId: string;
  fractionCount: number;
  attachedFee: number;
  signature: string;
}): Promise<RPCResult> {
  return rpcPost("BuyFractions", params);
}

// ─── SECONDARY MARKET ───────────────────────────────────────

export async function createListing(params: {
  sender: string;
  assetId: string;
  fractions: number;
  askPricePerFraction: number;
  signature: string;
}): Promise<RPCResult<string>> {
  return rpcPost<string>("CreateListing", params);
}

export async function fillListing(params: {
  sender: string;
  listingId: string;
  fractionsToBuy: number;
  attachedFee: number;
  signature: string;
}): Promise<RPCResult> {
  return rpcPost("FillListing", params);
}

export async function cancelListing(params: {
  sender: string;
  listingId: string;
  signature: string;
}): Promise<RPCResult> {
  return rpcPost("CancelListing", params);
}

// ─── YIELD ──────────────────────────────────────────────────

export async function distributeYield(params: {
  sender: string;
  assetId: string;
  yieldAmount: number;
  signature: string;
}): Promise<RPCResult> {
  return rpcPost("DistributeYield", params);
}

export async function claimYield(params: {
  sender: string;
  signature: string;
}): Promise<RPCResult<number>> {
  return rpcPost<number>("ClaimYield", params);
}

// ─── GOVERNANCE ─────────────────────────────────────────────

export async function createProposal(params: {
  sender: string;
  assetId: string;
  title: string;
  description: string;
  signature: string;
}): Promise<RPCResult<string>> {
  return rpcPost<string>("CreateProposal", params);
}

export async function castVote(params: {
  sender: string;
  proposalId: string;
  support: boolean;
  signature: string;
}): Promise<RPCResult> {
  return rpcPost("CastVote", params);
}

// ─── QUERIES ────────────────────────────────────────────────

export async function getAsset(assetId: string): Promise<RPCResult> {
  return rpcGet("GetAsset", { assetId });
}

export async function getRegistry(): Promise<RPCResult<string[]>> {
  return rpcGet<string[]>("GetRegistry");
}

export async function getPortfolio(address: string): Promise<RPCResult> {
  return rpcGet("GetPortfolio", { address });
}

export async function getMarket(): Promise<RPCResult<string[]>> {
  return rpcGet<string[]>("GetMarket");
}

export async function getListing(listingId: string): Promise<RPCResult> {
  return rpcGet("GetListing", { listingId });
}

export async function getProposal(proposalId: string): Promise<RPCResult> {
  return rpcGet("GetProposal", { proposalId });
}

export async function getTreasury(): Promise<RPCResult<number>> {
  return rpcGet<number>("GetTreasury");
}

// ─── UTILS ──────────────────────────────────────────────────

export async function sha256(text: string): Promise<string> {
  const buf = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text));
  return Array.from(new Uint8Array(buf))
    .map(b => b.toString(16).padStart(2, "0"))
    .join("");
}

export function formatCNPY(nCNPY: number): string {
  return (nCNPY / 1_000_000).toFixed(4) + " CNPY";
}

export function formatUSD(cents: number): string {
  return "$" + (cents / 100).toLocaleString("en-US", { minimumFractionDigits: 2 });
}
