package nepse

import (
	"encoding/json"
	"fmt"

	"nepse-api/src/shared/nepse/assets"
)

// Endpoint names an entry of the embedded API_ENDPOINTS.json.
type Endpoint string

const (
	EpSummary           Endpoint = "summary_url"
	EpSupplyDemand      Endpoint = "supply_demand_url"
	EpTopGainers        Endpoint = "top_gainers_url"
	EpTopLosers         Endpoint = "top_losers_url"
	EpTopTrade          Endpoint = "top_ten_trade_url"
	EpTopTransaction    Endpoint = "top_ten_transaction_url"
	EpTopTurnover       Endpoint = "top_ten_turnover_url"
	EpMarketStatus      Endpoint = "nepse_open_url"
	EpIndex             Endpoint = "nepse_index_url"
	EpSubIndices        Endpoint = "nepse_subindices_url"
	EpCompanyList       Endpoint = "company_list_url"
	EpSecurityList      Endpoint = "security_list_url"
	EpPriceVolume       Endpoint = "price_volume_url"
	EpLiveMarket        Endpoint = "live-market"
	EpMarketDepth       Endpoint = "market-depth"
	EpCompanyDetails    Endpoint = "company_details"
	EpCompanyDailyGraph Endpoint = "company_daily_graph"
	EpPriceHistory      Endpoint = "company_price_volume_history"
	EpFloorSheet        Endpoint = "floor_sheet"
)

// shapeKind is the JSON structure an endpoint must return. The shape is checked before a response is cached, so a malformed or unexpected body can never be stored and replayed.
type shapeKind int

const (
	shapeAny        shapeKind = iota // any valid JSON
	shapeArray                       // JSON array
	shapeObjects                     // JSON array whose elements are objects
	shapeObject                      // JSON object
	shapeCollection                  // array or object
)

var endpointShapes = map[Endpoint]shapeKind{
	EpSummary: shapeObjects, EpSupplyDemand: shapeCollection, EpTopGainers: shapeArray, EpTopLosers: shapeArray,
	EpTopTrade: shapeArray, EpTopTransaction: shapeArray, EpTopTurnover: shapeArray, EpMarketStatus: shapeObject,
	EpIndex: shapeObjects, EpSubIndices: shapeObjects, EpCompanyList: shapeObjects, EpSecurityList: shapeObjects,
	EpPriceVolume: shapeObjects, EpLiveMarket: shapeArray,
}

// IndexGraphs maps public names to upstream endpoints (main indices).
var IndexGraphs = map[string]Endpoint{
	"nepse":           "nepse_index_daily_graph",
	"sensitive":       "sensitive_index_daily_graph",
	"float":           "float_index_daily_graph",
	"sensitive-float": "sensitive_float_index_daily_graph",
}

// SubIndexGraphs maps sector slugs to upstream sub-index graph endpoints.
var SubIndexGraphs = map[string]Endpoint{
	"banking":            "banking_sub_index_graph",
	"development-bank":   "development_bank_sub_index_graph",
	"finance":            "finance_sub_index_graph",
	"hotels-tourism":     "hotel_tourism_sub_index_graph",
	"hydropower":         "hydro_sub_index_graph",
	"investment":         "investment_sub_index_graph",
	"life-insurance":     "life_insurance_sub_index_graph",
	"manufacturing":      "manufacturing_sub_index_graph",
	"microfinance":       "microfinance_sub_index_graph",
	"mutual-fund":        "mutual_fund_sub_index_graph",
	"non-life-insurance": "non_life_insurance_sub_index_graph",
	"others":             "others_sub_index_graph",
	"trading":            "trading_sub_index_graph",
}

// assetsData is everything parsed from the embedded files.
type assetsData struct {
	endpoints    map[string]string
	payloadSeeds []int
	headers      map[string]string
}

func loadAssets() (assetsData, error) {
	var a assetsData
	for _, f := range []struct {
		name string
		raw  []byte
		dst  any
	}{
		{"API_ENDPOINTS.json", assets.EndpointsJSON(), &a.endpoints},
		{"PAYLOAD_SEEDS.json", assets.PayloadSeedsJSON(), &a.payloadSeeds},
		{"HEADERS.json", assets.HeadersJSON(), &a.headers},
	} {
		if err := json.Unmarshal(f.raw, f.dst); err != nil {
			return assetsData{}, fmt.Errorf("parse %s: %w", f.name, err)
		}
	}
	for _, ep := range []Endpoint{EpSummary, EpSupplyDemand, EpTopGainers, EpTopLosers, EpTopTrade, EpTopTransaction, EpTopTurnover,
		EpMarketStatus, EpIndex, EpSubIndices, EpCompanyList, EpSecurityList, EpPriceVolume, EpLiveMarket, EpMarketDepth,
		EpCompanyDetails, EpCompanyDailyGraph, EpPriceHistory, EpFloorSheet} {
		if a.endpoints[string(ep)] == "" {
			return assetsData{}, fmt.Errorf("API_ENDPOINTS.json lacks %q", ep)
		}
	}
	for name, ep := range IndexGraphs {
		if a.endpoints[string(ep)] == "" {
			return assetsData{}, fmt.Errorf("API_ENDPOINTS.json lacks graph %q (%s)", name, ep)
		}
	}
	for name, ep := range SubIndexGraphs {
		if a.endpoints[string(ep)] == "" {
			return assetsData{}, fmt.Errorf("API_ENDPOINTS.json lacks sub-index graph %q (%s)", name, ep)
		}
	}
	return a, nil
}
