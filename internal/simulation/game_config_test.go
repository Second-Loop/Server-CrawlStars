package simulation

import (
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	clientconfig "github.com/Second-Loop/Server-CrawlStars/client-config"
)

func TestClientGameConfigSharedCollisionValuesMatchSimulation(t *testing.T) {
	config := loadClientSharedGameConfig(t)

	if config.Version != clientconfig.Version {
		t.Fatalf("expected client config version %d, got %d", clientconfig.Version, config.Version)
	}
	if config.TileSize != TileSize {
		t.Fatalf("expected tile size %f, got %f", TileSize, config.TileSize)
	}
	if config.PlayerRadius != DefaultPlayerRadius {
		t.Fatalf("expected player radius %f, got %f", DefaultPlayerRadius, config.PlayerRadius)
	}
	if config.ProjectileRadius != DefaultProjectileRadius {
		t.Fatalf("expected projectile radius %f, got %f", DefaultProjectileRadius, config.ProjectileRadius)
	}
}

func TestServerGameConfigArtifactMatchesServerSimulationConstants(t *testing.T) {
	config := loadServerGameConfig(t)

	if config.Version != ServerGameConfigVersion {
		t.Fatalf("expected server config version %d, got %d", ServerGameConfigVersion, config.Version)
	}
	if config.TickRate != TickRate {
		t.Fatalf("expected tick rate %d, got %d", TickRate, config.TickRate)
	}
	if config.Tile.Size != TileSize {
		t.Fatalf("expected tile size %f, got %f", TileSize, config.Tile.Size)
	}
	if len(config.Player.Types) != 3 {
		t.Fatalf("expected three player types, got %+v", config.Player.Types)
	}
	for characterType, wantHP := range map[CharacterType]float64{
		CharacterTypeShelly: 4000,
		CharacterTypeColt:   3100,
		CharacterTypeLily:   4100,
	} {
		player, ok := config.PlayerType(characterType)
		if !ok {
			t.Fatalf("missing player type %d", characterType)
		}
		if player.Radius != DefaultPlayerRadius {
			t.Fatalf("player %q radius = %f, want %f", player.ID, player.Radius, DefaultPlayerRadius)
		}
		if player.HP != wantHP {
			t.Fatalf("player %q HP = %f, want %f", player.ID, player.HP, wantHP)
		}
		if player.Speed != DefaultPlayerSpeed {
			t.Fatalf("player %q speed = %f, want %f", player.ID, player.Speed, DefaultPlayerSpeed)
		}
	}
	if len(config.Projectile.Types) != 1 {
		t.Fatalf("expected one projectile type, got %+v", config.Projectile.Types)
	}
	if config.Projectile.Types[0].ID != "default" {
		t.Fatalf("expected default projectile type, got %+v", config.Projectile.Types[0])
	}
	if config.Projectile.Types[0].Radius != DefaultProjectileRadius {
		t.Fatalf("expected projectile radius %f, got %f", DefaultProjectileRadius, config.Projectile.Types[0].Radius)
	}
	if config.Projectile.Types[0].Speed != DefaultProjectileSpeed {
		t.Fatalf("expected projectile speed %f, got %f", DefaultProjectileSpeed, config.Projectile.Types[0].Speed)
	}
}

func TestLoadServerGameConfigIncludesCharacterNormalAttacks(t *testing.T) {
	config := loadServerGameConfig(t)
	if config.Version != ServerGameConfigVersion {
		t.Fatalf("server version = %d, want %d", config.Version, ServerGameConfigVersion)
	}
	wants := map[CharacterType]NormalAttackConfig{
		CharacterTypeShelly: {Kind: NormalAttackSpreadProjectile, DamagePerHit: 280, RangeTiles: 7.2, MaxCharges: 3, RechargeTicks: 30, Projectile: &ProjectileAttackConfig{Type: "default", Count: 5, DirectionOffsetsDegrees: []float64{-12, -6, 0, 6, 12}}},
		CharacterTypeColt:   {Kind: NormalAttackBurstProjectile, DamagePerHit: 340, RangeTiles: 9, MaxCharges: 3, RechargeTicks: 30, Projectile: &ProjectileAttackConfig{Type: "default", Count: 6, DirectionOffsetsDegrees: []float64{0}, IntervalTicks: 6}},
		CharacterTypeLily:   {Kind: NormalAttackMelee, DamagePerHit: 1100, RangeTiles: 2.2, MaxCharges: 2, RechargeTicks: 30},
	}
	for characterType, want := range wants {
		got, ok := config.PlayerType(characterType)
		if !ok {
			t.Fatalf("missing character type %d", characterType)
		}
		if !reflect.DeepEqual(got.NormalAttack, want) {
			t.Fatalf("character type %d normal attack = %#v, want %#v", characterType, got.NormalAttack, want)
		}
	}
}

func TestClientAndServerConfigVersionsAreIndependent(t *testing.T) {
	client := loadClientSharedGameConfig(t)
	server := loadServerGameConfig(t)
	if client.Version != clientconfig.Version || server.Version != ServerGameConfigVersion {
		t.Fatalf("versions = client %d server %d, want %d/%d", client.Version, server.Version, clientconfig.Version, ServerGameConfigVersion)
	}
}

func TestClientAndServerCharacterCatalogMappingsMatch(t *testing.T) {
	client := loadClientSharedGameConfig(t)
	server := loadServerGameConfig(t)
	want := map[CharacterType]bool{
		CharacterTypeShelly: true,
		CharacterTypeColt:   true,
		CharacterTypeLily:   true,
	}
	if client.Version != clientconfig.Version || server.Version != ServerGameConfigVersion {
		t.Fatalf("client/server version = %d/%d, want %d/%d", client.Version, server.Version, clientconfig.Version, ServerGameConfigVersion)
	}
	clientMapping := make(map[CharacterType]bool, len(client.Characters))
	for _, character := range client.Characters {
		clientMapping[CharacterType(character.Type)] = true
	}
	serverMapping := make(map[CharacterType]bool, len(server.Player.Types))
	for _, playerType := range server.Player.Types {
		serverMapping[playerType.CharacterType] = true
	}
	if len(client.Characters) != len(want) || len(clientMapping) != len(client.Characters) {
		t.Fatalf("client character catalog is not exact/unique: entries=%d mapping=%v", len(client.Characters), clientMapping)
	}
	if len(server.Player.Types) != len(want) || len(serverMapping) != len(server.Player.Types) {
		t.Fatalf("server character catalog is not exact/unique: entries=%d mapping=%v", len(server.Player.Types), serverMapping)
	}
	if !reflect.DeepEqual(clientMapping, want) || !reflect.DeepEqual(serverMapping, want) {
		t.Fatalf("character mapping drift: client=%v server=%v want=%v", clientMapping, serverMapping, want)
	}
}

func TestGameConfigPlayerTypeLookupIsIndependentOfCatalogOrder(t *testing.T) {
	config := StaticGameConfig()
	slices.Reverse(config.Player.Types)
	for characterType, wantHP := range map[CharacterType]float64{
		CharacterTypeShelly: 4000,
		CharacterTypeColt:   3100,
		CharacterTypeLily:   4100,
	} {
		got, ok := config.PlayerType(characterType)
		if !ok || got.HP != wantHP {
			t.Fatalf("PlayerType(%d) = %+v, %t; want HP %v", characterType, got, ok, wantHP)
		}
	}
	if got := config.DefaultPlayerType(); got.CharacterType != CharacterTypeShelly || got.ID != "shelly" {
		t.Fatalf("DefaultPlayerType() = %+v, want Shelly", got)
	}
}

func TestResolveGameConfigRejectsUnsupportedVersion(t *testing.T) {
	config := StaticGameConfig()
	config.Version = 1
	if _, err := ResolveGameConfig(config); err == nil || !strings.Contains(err.Error(), "version must be 3") {
		t.Fatalf("ResolveGameConfig(version 1) error = %v, want exact-version rejection", err)
	}
}

func TestPlayerTypeConfigRejectsMissingOrNullCharacterType(t *testing.T) {
	for name, payload := range map[string]string{
		"missing": `{"id":"shelly","radius":0.5,"hp":4000,"speed":2,"maxAttackCharges":4,"attackRechargeTicks":30}`,
		"null":    `{"characterType":null,"id":"shelly","radius":0.5,"hp":4000,"speed":2,"maxAttackCharges":4,"attackRechargeTicks":30}`,
	} {
		t.Run(name, func(t *testing.T) {
			var playerType PlayerTypeConfig
			if err := json.Unmarshal([]byte(payload), &playerType); err == nil {
				t.Fatal("expected missing/null characterType to fail")
			}
		})
	}
}

func TestResolveGameConfigRejectsInvalidCharacterCatalog(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*GameConfig)
	}{
		{"duplicate numeric", func(c *GameConfig) { c.Player.Types[1].CharacterType = CharacterTypeShelly }},
		{"duplicate string", func(c *GameConfig) { c.Player.Types[1].ID = "shelly" }},
		{"missing lily", func(c *GameConfig) { c.Player.Types = c.Player.Types[:2] }},
		{"unknown numeric", func(c *GameConfig) { c.Player.Types[2].CharacterType = CharacterType(3) }},
		{"stable mapping drift", func(c *GameConfig) { c.Player.Types[1].ID = "lily" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := StaticGameConfig()
			tt.mutate(&config)
			if _, err := ResolveGameConfig(config); err == nil || (!strings.Contains(err.Error(), "character") && !strings.Contains(err.Error(), "player type")) {
				t.Fatalf("ResolveGameConfig() error = %v, want character catalog rejection", err)
			}
		})
	}
}

func TestResolveGameConfigRejectsInvalidNormalAttackCombinations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*GameConfig)
	}{
		{"unknown kind", func(c *GameConfig) { c.Player.Types[0].NormalAttack.Kind = "unknown" }},
		{"zero damage", func(c *GameConfig) { c.Player.Types[0].NormalAttack.DamagePerHit = 0 }},
		{"nan range", func(c *GameConfig) { c.Player.Types[0].NormalAttack.RangeTiles = math.NaN() }},
		{"zero charges", func(c *GameConfig) { c.Player.Types[0].NormalAttack.MaxCharges = 0 }},
		{"zero recharge", func(c *GameConfig) { c.Player.Types[0].NormalAttack.RechargeTicks = 0 }},
		{"missing projectile", func(c *GameConfig) { c.Player.Types[0].NormalAttack.Projectile = nil }},
		{"melee projectile", func(c *GameConfig) {
			p := c.Player.Types[0].NormalAttack.Projectile
			c.Player.Types[2].NormalAttack.Projectile = p
		}},
		{"unknown projectile reference", func(c *GameConfig) { c.Player.Types[0].NormalAttack.Projectile.Type = "missing" }},
		{"duplicate projectile id", func(c *GameConfig) { c.Projectile.Types = append(c.Projectile.Types, c.Projectile.Types[0]) }},
		{"spread zero count", func(c *GameConfig) {
			c.Player.Types[0].NormalAttack.Projectile.Count = 0
			c.Player.Types[0].NormalAttack.Projectile.DirectionOffsetsDegrees = nil
		}},
		{"spread count mismatch", func(c *GameConfig) { c.Player.Types[0].NormalAttack.Projectile.Count = 4 }},
		{"spread nan offset", func(c *GameConfig) { c.Player.Types[0].NormalAttack.Projectile.DirectionOffsetsDegrees[0] = math.NaN() }},
		{"spread positive infinite offset", func(c *GameConfig) {
			c.Player.Types[0].NormalAttack.Projectile.DirectionOffsetsDegrees[1] = math.Inf(1)
		}},
		{"spread negative infinite offset", func(c *GameConfig) {
			c.Player.Types[0].NormalAttack.Projectile.DirectionOffsetsDegrees[2] = math.Inf(-1)
		}},
		{"spread interval", func(c *GameConfig) { c.Player.Types[0].NormalAttack.Projectile.IntervalTicks = 1 }},
		{"burst count", func(c *GameConfig) { c.Player.Types[1].NormalAttack.Projectile.Count = 1 }},
		{"burst offset", func(c *GameConfig) { c.Player.Types[1].NormalAttack.Projectile.DirectionOffsetsDegrees = []float64{1} }},
		{"burst nan offset", func(c *GameConfig) {
			c.Player.Types[1].NormalAttack.Projectile.DirectionOffsetsDegrees = []float64{math.NaN()}
		}},
		{"burst infinite offset", func(c *GameConfig) {
			c.Player.Types[1].NormalAttack.Projectile.DirectionOffsetsDegrees = []float64{math.Inf(1)}
		}},
		{"burst interval", func(c *GameConfig) { c.Player.Types[1].NormalAttack.Projectile.IntervalTicks = 0 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := StaticGameConfig()
			tt.mutate(&config)
			if _, err := ResolveGameConfig(config); err == nil {
				t.Fatal("expected normal attack combination to be rejected")
			}
		})
	}
}

func TestServerGameConfigModeCatalog(t *testing.T) {
	config := loadServerGameConfig(t)
	want := map[string]GameModeConfig{
		GameModeDuel1v1: {
			ID:              GameModeDuel1v1,
			PlayersPerMatch: 2,
			Teams:           []TeamConfig{{Name: TeamRed, Size: 1}, {Name: TeamBlue, Size: 1}},
			Rules:           GameModeRulesConfig{TeamBehavior: TeamBehaviorTwoTeams, FriendlyFire: false},
		},
		GameModeSolo: {
			ID:              GameModeSolo,
			PlayersPerMatch: 6,
			Teams:           []TeamConfig{{Name: Team("solo-1"), Size: 1}, {Name: Team("solo-2"), Size: 1}, {Name: Team("solo-3"), Size: 1}, {Name: Team("solo-4"), Size: 1}, {Name: Team("solo-5"), Size: 1}, {Name: Team("solo-6"), Size: 1}},
			Rules:           GameModeRulesConfig{TeamBehavior: TeamBehaviorFreeForAll, FriendlyFire: false},
		},
		GameModeTeam: {
			ID:              GameModeTeam,
			PlayersPerMatch: 6,
			Teams:           []TeamConfig{{Name: TeamRed, Size: 3}, {Name: TeamBlue, Size: 3}},
			Rules:           GameModeRulesConfig{TeamBehavior: TeamBehaviorTwoTeams, FriendlyFire: false},
		},
	}

	if config.ModeCatalog.Default != GameModeDuel1v1 {
		t.Fatalf("expected default mode %q, got %q", GameModeDuel1v1, config.ModeCatalog.Default)
	}
	if len(config.ModeCatalog.Catalog) != len(want) {
		t.Fatalf("expected exactly %d modes, got %+v", len(want), config.ModeCatalog.Catalog)
	}
	for _, got := range config.ModeCatalog.Catalog {
		wantMode, ok := want[got.ID]
		if !ok {
			t.Fatalf("unexpected mode %q in catalog", got.ID)
		}
		if !reflect.DeepEqual(got, wantMode) {
			t.Fatalf("mode %q mismatch:\n got: %+v\nwant: %+v", got.ID, got, wantMode)
		}
		delete(want, got.ID)
	}
	if len(want) != 0 {
		t.Fatalf("missing catalog modes: %+v", want)
	}
	if config.SelectedMode.ID != GameModeDuel1v1 {
		t.Fatalf("expected selected default mode %q, got %+v", GameModeDuel1v1, config.SelectedMode)
	}
}

func TestSelectModeSelectsRuntimeModeWithoutMutatingOriginalConfig(t *testing.T) {
	config := StaticGameConfig()
	wantOriginal := StaticGameConfig()

	for _, modeID := range []string{GameModeSolo, GameModeTeam} {
		t.Run(modeID, func(t *testing.T) {
			selected, err := config.SelectMode(modeID)
			if err != nil {
				t.Fatalf("select mode %q: %v", modeID, err)
			}
			var wantSelected GameModeConfig
			for _, mode := range config.ModeCatalog.Catalog {
				if mode.ID == modeID {
					wantSelected = mode
					break
				}
			}
			if !reflect.DeepEqual(selected.SelectedMode, wantSelected) {
				t.Fatalf("selected mode mismatch:\n got: %+v\nwant: %+v", selected.SelectedMode, wantSelected)
			}
			if !reflect.DeepEqual(config, wantOriginal) {
				t.Fatalf("SelectMode mutated original config:\n got: %+v\nwant: %+v", config, wantOriginal)
			}
		})
	}
}

func TestSelectModeRejectsUnknownMode(t *testing.T) {
	selected, err := StaticGameConfig().SelectMode("unknown")
	if err == nil {
		t.Fatal("expected unknown mode to be rejected")
	}
	if !strings.Contains(err.Error(), `unknown game mode "unknown"`) {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(selected, GameConfig{}) {
		t.Fatalf("expected zero config after failed selection, got %+v", selected)
	}
}

func TestResolveGameConfigCanonicalizesSelectedRuntimeMode(t *testing.T) {
	for _, modeID := range []string{GameModeSolo, GameModeTeam} {
		t.Run(modeID, func(t *testing.T) {
			selected, err := StaticGameConfig().SelectMode(modeID)
			if err != nil {
				t.Fatalf("select mode %q: %v", modeID, err)
			}
			canonicalMode := selected.SelectedMode
			selected.SelectedMode = GameModeConfig{
				ID:              modeID,
				PlayersPerMatch: 1,
				Teams:           []TeamConfig{{Name: Team("tampered"), Size: 1}},
				Rules: GameModeRulesConfig{
					TeamBehavior: "tampered",
					FriendlyFire: true,
				},
			}

			resolved, err := ResolveGameConfig(selected)
			if err != nil {
				t.Fatalf("resolve selected mode %q: %v", modeID, err)
			}
			if !reflect.DeepEqual(resolved.SelectedMode, canonicalMode) {
				t.Fatalf("expected resolved mode to use canonical catalog entry:\n got: %+v\nwant: %+v", resolved.SelectedMode, canonicalMode)
			}

			stateResolved := resolveStateGameConfig(Config{Game: selected})
			if !reflect.DeepEqual(stateResolved.SelectedMode, canonicalMode) {
				t.Fatalf("expected state mode to use canonical catalog entry:\n got: %+v\nwant: %+v", stateResolved.SelectedMode, canonicalMode)
			}
		})
	}
}

func TestResolveGameConfigRejectsUnknownSelectedMode(t *testing.T) {
	config := StaticGameConfig()
	config.SelectedMode = GameModeConfig{ID: "unknown"}

	_, err := ResolveGameConfig(config)
	if err == nil {
		t.Fatal("expected unknown selected mode to be rejected")
	}
	if !strings.Contains(err.Error(), `unknown game mode "unknown"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveGameConfigWrapsUniqueSpawnCapacityError(t *testing.T) {
	config := StaticGameConfig()
	config.Map = MapData{
		Width:      4,
		Height:     4,
		Index:      0,
		MaxPlayers: 6,
		TileSize:   TileSize,
		Map: [][]TileType{
			{TileWall, TileWall, TileWall, TileWall},
			{TileWall, TileSpawnPoint, TileWall, TileWall},
			{TileWall, TileWater, TileWall, TileWall},
			{TileWall, TileWall, TileWall, TileWall},
		},
	}

	_, err := ResolveGameConfig(config)
	if err == nil {
		t.Fatal("expected insufficient unique spawn capacity to be rejected")
	}
	if got, want := err.Error(), "resolve game config map: map maxPlayers 6 exceeds unique spawn capacity 1"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestServerGameConfigArtifactIncludesRuntimeMap(t *testing.T) {
	config := loadServerGameConfig(t)
	gameMap, err := ResolveMapData(config.Map)
	if err != nil {
		t.Fatalf("resolve server game config map: %v", err)
	}

	if gameMap.Width != 20 || gameMap.Height != 20 {
		t.Fatalf("expected 20x20 runtime map, got %dx%d", gameMap.Width, gameMap.Height)
	}
	if gameMap.MaxPlayers != 6 {
		t.Fatalf("expected map maxPlayers 6, got %d", gameMap.MaxPlayers)
	}
	if gameMap.TileSize != TileSize {
		t.Fatalf("expected map tile size %f, got %f", TileSize, gameMap.TileSize)
	}
}

func TestServerGameConfigArtifactMatchesClientMap0(t *testing.T) {
	config := loadServerGameConfig(t)
	want := expectedClientMap0()
	if !reflect.DeepEqual(config.Map.Map, want) {
		t.Fatalf("server runtime map drifted from SL-79 client Map_0:\n got: %+v\nwant: %+v", config.Map.Map, want)
	}
}

func expectedClientMap0() [][]TileType {
	return [][]TileType{
		{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
		{1, 2, 0, 0, 3, 3, 3, 0, 0, 0, 0, 0, 0, 3, 3, 3, 0, 0, 2, 1},
		{1, 0, 0, 1, 1, 3, 3, 0, 4, 4, 0, 0, 3, 3, 1, 1, 0, 0, 0, 1},
		{1, 0, 0, 1, 0, 3, 0, 0, 4, 4, 0, 0, 0, 3, 0, 1, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1},
		{1, 3, 3, 3, 1, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 1, 3, 3, 3, 1},
		{1, 3, 3, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 3, 3, 1},
		{1, 0, 3, 0, 0, 0, 0, 1, 1, 0, 0, 1, 1, 0, 0, 0, 0, 3, 0, 1},
		{1, 0, 0, 0, 4, 4, 4, 0, 0, 0, 0, 0, 0, 4, 4, 4, 0, 0, 0, 1},
		{1, 2, 0, 0, 0, 4, 4, 0, 0, 0, 0, 0, 0, 4, 4, 0, 0, 0, 2, 1},
		{1, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 0, 1, 4, 4, 1, 0, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 1, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 1, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 1, 1, 0, 0, 0, 0, 0, 4, 4, 0, 0, 1, 1, 0, 0, 0, 1},
		{1, 2, 0, 0, 3, 3, 3, 0, 0, 0, 4, 4, 0, 3, 3, 3, 0, 0, 2, 1},
		{1, 0, 0, 0, 3, 3, 0, 0, 0, 0, 0, 0, 0, 0, 3, 3, 0, 0, 0, 1},
		{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
	}
}

func TestResolveGameConfigRejectsInvalidModeCatalog(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*GameConfig)
		wantErr string
	}{
		{
			name: "empty mode id",
			mutate: func(config *GameConfig) {
				config.ModeCatalog.Catalog[0].ID = ""
			},
			wantErr: "mode.id",
		},
		{
			name: "empty team name",
			mutate: func(config *GameConfig) {
				config.ModeCatalog.Catalog[0].Teams[0].Name = ""
			},
			wantErr: "team name",
		},
		{
			name: "duplicate mode id",
			mutate: func(config *GameConfig) {
				config.ModeCatalog.Catalog[1].ID = GameModeDuel1v1
			},
			wantErr: "duplicated",
		},
		{
			name: "missing default mode",
			mutate: func(config *GameConfig) {
				config.ModeCatalog.Default = "missing"
			},
			wantErr: "default",
		},
		{
			name: "team size sum mismatch",
			mutate: func(config *GameConfig) {
				config.ModeCatalog.Catalog[2].Teams[0].Size = 2
			},
			wantErr: "team size total",
		},
		{
			name: "mode exceeds map capacity",
			mutate: func(config *GameConfig) {
				config.Map.MaxPlayers = 5
			},
			wantErr: "map.maxPlayers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := StaticGameConfig()
			tt.mutate(&config)

			_, err := ResolveGameConfig(config)
			if err == nil {
				t.Fatal("expected config to be rejected")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error to contain %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestValidateGameConfigRejectsUnsupportedTeamBehavior(t *testing.T) {
	config := StaticGameConfig()
	config.ModeCatalog.Catalog[0].Rules.TeamBehavior = "unsupported"

	_, err := ResolveGameConfig(config)
	if err == nil {
		t.Fatal("expected unsupported team behavior to be rejected")
	}
	if !strings.Contains(err.Error(), `teamBehavior "unsupported" is not supported`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGameConfigAssignsDefaultOneVsOneMatchTeams(t *testing.T) {
	config := StaticGameConfig()

	team, slot, ok := config.MatchTeamForPlayerIndex(0)
	if !ok || team != TeamRed || slot != 0 {
		t.Fatalf("expected player index 0 to be red slot 0, got team=%q slot=%d ok=%v", team, slot, ok)
	}
	team, slot, ok = config.MatchTeamForPlayerIndex(1)
	if !ok || team != TeamBlue || slot != 0 {
		t.Fatalf("expected player index 1 to be blue slot 0, got team=%q slot=%d ok=%v", team, slot, ok)
	}
	if team, slot, ok = config.MatchTeamForPlayerIndex(2); ok {
		t.Fatalf("expected player index 2 to be outside active 1v1 match, got team=%q slot=%d", team, slot)
	}
}

func TestGameConfigAssignsConfiguredMatchTeams(t *testing.T) {
	config := StaticGameConfig()
	config.SelectedMode = GameModeConfig{
		ID:              "test_quartet",
		PlayersPerMatch: 4,
		Teams: []TeamConfig{
			{Name: TeamRed, Size: 3},
			{Name: TeamBlue, Size: 1},
		},
		Rules: GameModeRulesConfig{
			TeamBehavior: TeamBehaviorTwoTeams,
			FriendlyFire: false,
		},
	}

	tests := []struct {
		index int
		team  Team
		slot  int
	}{
		{index: 0, team: TeamRed, slot: 0},
		{index: 1, team: TeamBlue, slot: 0},
		{index: 2, team: TeamRed, slot: 1},
		{index: 3, team: TeamRed, slot: 2},
	}
	for _, tt := range tests {
		team, slot, ok := config.MatchTeamForPlayerIndex(tt.index)
		if !ok || team != tt.team || slot != tt.slot {
			t.Fatalf("expected player index %d to be %s slot %d, got team=%q slot=%d ok=%v", tt.index, tt.team, tt.slot, team, slot, ok)
		}
	}

	if team, slot, ok := config.MatchTeamForPlayerIndex(4); ok {
		t.Fatalf("expected player index 4 to be outside active match, got team=%q slot=%d", team, slot)
	}
}

func loadClientSharedGameConfig(t *testing.T) clientconfig.Config {
	t.Helper()

	data, err := io.ReadAll(clientconfig.Reader())
	if err != nil {
		t.Fatalf("read embedded client config: %v", err)
	}

	config, err := clientconfig.Parse(data)
	if err != nil {
		t.Fatalf("parse embedded client config: %v", err)
	}
	return config
}

func loadServerGameConfig(t *testing.T) GameConfig {
	t.Helper()

	path := filepath.Join("..", "..", "server-config", "game-config.json")
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer file.Close()

	config, err := LoadGameConfig(file)
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	return config
}
