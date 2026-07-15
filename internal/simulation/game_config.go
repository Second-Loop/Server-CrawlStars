package simulation

import (
	"encoding/json"
	"fmt"
	"io"
)

type GameConfig struct {
	Version      int                     `json:"version"`
	TickRate     int                     `json:"tickRate"`
	Tile         TileConfig              `json:"tile"`
	Player       PlayerTypeSetConfig     `json:"player"`
	Projectile   ProjectileTypeSetConfig `json:"projectile"`
	ModeCatalog  GameModeCatalogConfig   `json:"mode"`
	SelectedMode GameModeConfig          `json:"-"`
	Map          MapData                 `json:"map"`
}

type TileConfig struct {
	Size float64 `json:"size"`
}

type PlayerTypeSetConfig struct {
	Types []PlayerTypeConfig `json:"types"`
}

type PlayerTypeConfig struct {
	ID                  string  `json:"id"`
	Radius              float64 `json:"radius"`
	HP                  float64 `json:"hp"`
	Speed               float64 `json:"speed"`
	MaxAttackCharges    int     `json:"maxAttackCharges"`
	AttackRechargeTicks int     `json:"attackRechargeTicks"`
}

type ProjectileTypeSetConfig struct {
	Types []ProjectileTypeConfig `json:"types"`
}

type ProjectileTypeConfig struct {
	ID     string  `json:"id"`
	Radius float64 `json:"radius"`
	Damage float64 `json:"damage"`
	Speed  float64 `json:"speed"`
}

type GameModeConfig struct {
	ID              string              `json:"id"`
	PlayersPerMatch int                 `json:"playersPerMatch"`
	Teams           []TeamConfig        `json:"teams"`
	Rules           GameModeRulesConfig `json:"rules"`
}

type GameModeCatalogConfig struct {
	Default string           `json:"default"`
	Catalog []GameModeConfig `json:"catalog"`
}

type TeamConfig struct {
	Name Team `json:"name"`
	Size int  `json:"size"`
}

type GameModeRulesConfig struct {
	TeamBehavior string `json:"teamBehavior"`
	FriendlyFire bool   `json:"friendlyFire"`
}

const (
	GameModeDuel1v1        = "duel_1v1"
	GameModeSolo           = "solo"
	GameModeTeam           = "team"
	TeamBehaviorTwoTeams   = "two_teams"
	TeamBehaviorFreeForAll = "free_for_all"
)

func (config GameConfig) SelectMode(id string) (GameConfig, error) {
	for _, mode := range config.ModeCatalog.Catalog {
		if mode.ID == id {
			selected := config
			selected.SelectedMode = mode
			return selected, nil
		}
	}
	return GameConfig{}, fmt.Errorf("unknown game mode %q", id)
}

func LoadGameConfig(reader io.Reader) (GameConfig, error) {
	var config GameConfig
	if err := json.NewDecoder(reader).Decode(&config); err != nil {
		return GameConfig{}, fmt.Errorf("decode game config: %w", err)
	}
	return ResolveGameConfig(config)
}

func ResolveGameConfig(config GameConfig) (GameConfig, error) {
	if config.Version <= 0 {
		return GameConfig{}, fmt.Errorf("game config version must be positive")
	}
	if config.TickRate <= 0 {
		return GameConfig{}, fmt.Errorf("game config tickRate must be positive")
	}
	if config.Tile.Size <= 0 {
		return GameConfig{}, fmt.Errorf("game config tile.size must be positive")
	}
	if len(config.Player.Types) == 0 {
		return GameConfig{}, fmt.Errorf("game config player.types must not be empty")
	}
	for _, player := range config.Player.Types {
		if player.ID == "" {
			return GameConfig{}, fmt.Errorf("game config player type id must not be empty")
		}
		if player.Radius <= 0 || player.HP <= 0 || player.Speed <= 0 {
			return GameConfig{}, fmt.Errorf("game config player type %q values must be positive", player.ID)
		}
		if player.MaxAttackCharges <= 0 {
			return GameConfig{}, fmt.Errorf("game config player type %q maxAttackCharges must be positive", player.ID)
		}
		if player.AttackRechargeTicks <= 0 {
			return GameConfig{}, fmt.Errorf("game config player type %q attackRechargeTicks must be positive", player.ID)
		}
	}
	if len(config.Projectile.Types) == 0 {
		return GameConfig{}, fmt.Errorf("game config projectile.types must not be empty")
	}
	for _, projectile := range config.Projectile.Types {
		if projectile.ID == "" {
			return GameConfig{}, fmt.Errorf("game config projectile type id must not be empty")
		}
		if projectile.Radius <= 0 || projectile.Damage <= 0 || projectile.Speed <= 0 {
			return GameConfig{}, fmt.Errorf("game config projectile type %q values must be positive", projectile.ID)
		}
	}
	if err := validateGameModeCatalogConfig(config.ModeCatalog); err != nil {
		return GameConfig{}, err
	}
	gameMap := config.Map
	if gameMap.TileSize <= 0 {
		gameMap.TileSize = config.Tile.Size
	}
	resolvedMap, err := ResolveMapData(gameMap)
	if err != nil {
		return GameConfig{}, fmt.Errorf("resolve game config map: %w", err)
	}
	for _, mode := range config.ModeCatalog.Catalog {
		if mode.PlayersPerMatch > resolvedMap.MaxPlayers {
			return GameConfig{}, fmt.Errorf("game config mode %q playersPerMatch must be less than or equal to map.maxPlayers", mode.ID)
		}
	}
	config.Map = resolvedMap
	selectedModeID := config.SelectedMode.ID
	selectedModeSource := "selected"
	if selectedModeID == "" {
		selectedModeID = config.ModeCatalog.Default
		selectedModeSource = "default"
	}
	selected, err := config.SelectMode(selectedModeID)
	if err != nil {
		return GameConfig{}, fmt.Errorf("select game config mode.%s: %w", selectedModeSource, err)
	}
	return selected, nil
}

func validateGameModeCatalogConfig(catalog GameModeCatalogConfig) error {
	if catalog.Default == "" {
		return fmt.Errorf("game config mode.default must not be empty")
	}
	if len(catalog.Catalog) == 0 {
		return fmt.Errorf("game config mode.catalog must not be empty")
	}

	seenModes := make(map[string]bool, len(catalog.Catalog))
	for _, mode := range catalog.Catalog {
		if err := validateGameModeConfig(mode); err != nil {
			return err
		}
		if seenModes[mode.ID] {
			return fmt.Errorf("game config mode %q must not be duplicated", mode.ID)
		}
		seenModes[mode.ID] = true
	}
	if !seenModes[catalog.Default] {
		return fmt.Errorf("game config mode.default %q must reference a catalog mode", catalog.Default)
	}
	return nil
}

func validateGameModeConfig(mode GameModeConfig) error {
	if mode.ID == "" {
		return fmt.Errorf("game config mode.id must not be empty")
	}
	if mode.PlayersPerMatch <= 0 {
		return fmt.Errorf("game config mode.playersPerMatch must be positive")
	}
	if len(mode.Teams) == 0 {
		return fmt.Errorf("game config mode.teams must not be empty")
	}
	if mode.Rules.TeamBehavior == "" {
		return fmt.Errorf("game config mode.rules.teamBehavior must not be empty")
	}

	totalTeamSize := 0
	seenTeams := make(map[Team]bool, len(mode.Teams))
	for _, team := range mode.Teams {
		if team.Name == "" {
			return fmt.Errorf("game config mode team name must not be empty")
		}
		if seenTeams[team.Name] {
			return fmt.Errorf("game config mode team %q must not be duplicated", team.Name)
		}
		seenTeams[team.Name] = true
		if team.Size <= 0 {
			return fmt.Errorf("game config mode team %q size must be positive", team.Name)
		}
		totalTeamSize += team.Size
	}
	if totalTeamSize != mode.PlayersPerMatch {
		return fmt.Errorf("game config mode team size total must match playersPerMatch")
	}
	return nil
}

func StaticGameConfig() GameConfig {
	defaultMode := DefaultGameModeConfig()
	return GameConfig{
		Version:  1,
		TickRate: TickRate,
		Tile: TileConfig{
			Size: TileSize,
		},
		Player: PlayerTypeSetConfig{
			Types: []PlayerTypeConfig{
				{
					ID:                  "default",
					Radius:              DefaultPlayerRadius,
					HP:                  DefaultPlayerHP,
					Speed:               DefaultPlayerSpeed,
					MaxAttackCharges:    4,
					AttackRechargeTicks: 30,
				},
			},
		},
		Projectile: ProjectileTypeSetConfig{
			Types: []ProjectileTypeConfig{
				{
					ID:     "default",
					Radius: DefaultProjectileRadius,
					Damage: DefaultProjectileDamage,
					Speed:  DefaultProjectileSpeed,
				},
			},
		},
		ModeCatalog: GameModeCatalogConfig{
			Default: GameModeDuel1v1,
			Catalog: []GameModeConfig{
				defaultMode,
				{
					ID:              GameModeSolo,
					PlayersPerMatch: 6,
					Teams: []TeamConfig{
						{Name: Team("solo-1"), Size: 1},
						{Name: Team("solo-2"), Size: 1},
						{Name: Team("solo-3"), Size: 1},
						{Name: Team("solo-4"), Size: 1},
						{Name: Team("solo-5"), Size: 1},
						{Name: Team("solo-6"), Size: 1},
					},
					Rules: GameModeRulesConfig{
						TeamBehavior: TeamBehaviorFreeForAll,
						FriendlyFire: false,
					},
				},
				{
					ID:              GameModeTeam,
					PlayersPerMatch: 6,
					Teams: []TeamConfig{
						{Name: TeamRed, Size: 3},
						{Name: TeamBlue, Size: 3},
					},
					Rules: GameModeRulesConfig{
						TeamBehavior: TeamBehaviorTwoTeams,
						FriendlyFire: false,
					},
				},
			},
		},
		SelectedMode: defaultMode,
		Map:          StaticMapFixture(),
	}
}

func DefaultGameModeConfig() GameModeConfig {
	return GameModeConfig{
		ID:              GameModeDuel1v1,
		PlayersPerMatch: 2,
		Teams: []TeamConfig{
			{Name: TeamRed, Size: 1},
			{Name: TeamBlue, Size: 1},
		},
		Rules: GameModeRulesConfig{
			TeamBehavior: TeamBehaviorTwoTeams,
			FriendlyFire: false,
		},
	}
}

func (config GameConfig) DefaultPlayerType() PlayerTypeConfig {
	if len(config.Player.Types) == 0 {
		return StaticGameConfig().Player.Types[0]
	}
	return config.Player.Types[0]
}

func (config GameConfig) DefaultProjectileType() ProjectileTypeConfig {
	if len(config.Projectile.Types) == 0 {
		return StaticGameConfig().Projectile.Types[0]
	}
	return config.Projectile.Types[0]
}

func (config GameConfig) MatchPlayerCount() int {
	if config.SelectedMode.PlayersPerMatch <= 0 {
		return DefaultGameModeConfig().PlayersPerMatch
	}
	return config.SelectedMode.PlayersPerMatch
}

func (config GameConfig) MatchTeamForPlayerIndex(index int) (Team, int, bool) {
	if index < 0 || index >= config.MatchPlayerCount() {
		return "", 0, false
	}
	return matchTeamForPlayerIndex(index, config.SelectedMode.Teams)
}

func (config GameConfig) TeamForPlayerIndex(index int) (Team, int, bool) {
	if index < 0 {
		return "", 0, false
	}
	if index < config.MatchPlayerCount() {
		team, slot, ok := config.MatchTeamForPlayerIndex(index)
		if ok {
			return team, slot, true
		}
	}
	team, slot, ok := roomTeamForPlayerIndex(index, config.SelectedMode.Teams)
	if ok {
		return team, slot, true
	}
	return roomTeamForPlayerIndex(index, DefaultGameModeConfig().Teams)
}

func matchTeamForPlayerIndex(index int, teams []TeamConfig) (Team, int, bool) {
	if index < 0 || len(teams) == 0 {
		return "", 0, false
	}
	assigned := 0
	for slot := 0; ; slot++ {
		progressed := false
		for _, team := range teams {
			if slot >= team.Size {
				continue
			}
			if assigned == index {
				return team.Name, slot, team.Name != ""
			}
			assigned++
			progressed = true
		}
		if !progressed {
			return "", 0, false
		}
	}
}

func roomTeamForPlayerIndex(index int, teams []TeamConfig) (Team, int, bool) {
	if index < 0 || len(teams) == 0 {
		return "", 0, false
	}
	team := teams[index%len(teams)]
	if team.Name == "" {
		return "", 0, false
	}
	return team.Name, index / len(teams), true
}

func resolveStateGameConfig(config Config) GameConfig {
	gameConfig := config.Game
	hasConfigMap := config.Map.Width > 0 || config.Map.Height > 0 || len(config.Map.Map) > 0
	if gameConfig.Version <= 0 {
		gameConfig = StaticGameConfig()
		if !hasConfigMap {
			gameConfig.Map = MapData{}
		}
	}
	if hasConfigMap {
		gameConfig.Map = config.Map
	}
	if gameConfig.Map.Width > 0 || gameConfig.Map.Height > 0 || len(gameConfig.Map.Map) > 0 {
		resolved, err := ResolveGameConfig(gameConfig)
		if err != nil {
			return StaticGameConfig()
		}
		return resolved
	}
	return gameConfig
}
