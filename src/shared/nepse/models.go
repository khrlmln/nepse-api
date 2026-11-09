package nepse

import (
	"encoding/json"
	"time"
)

// AuthResponse is the upstream authentication ("prove") response. Its fields are credentials and must never be logged.
type AuthResponse struct {
	ServerTime   int64  `json:"serverTime"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	Salt1        int    `json:"salt1"`
	Salt2        int    `json:"salt2"`
	Salt3        int    `json:"salt3"`
	Salt4        int    `json:"salt4"`
	Salt5        int    `json:"salt5"`
}

type tokenState struct {
	access, refresh string
	fetchedAt       time.Time
	salts           [5]int
}

// FloorSheetPage is one upstream page of a floorsheet (500 rows per page). TotalPages and TotalElements are -1 when upstream did not report them.
type FloorSheetPage struct {
	Rows          []json.RawMessage
	Page          int
	TotalPages    int
	TotalElements int64
}

// FloorSheetPageSize is the fixed upstream page size.
const FloorSheetPageSize = 500
