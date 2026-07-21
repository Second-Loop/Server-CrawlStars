package simulation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
)

const (
	ClientGameConfigVersion = 2
	ServerGameConfigVersion = 3
)

type CharacterType int

const (
	CharacterTypeShelly CharacterType = 0
	CharacterTypeColt   CharacterType = 1
	CharacterTypeLily   CharacterType = 2
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

func expectedCharacterID(characterType CharacterType) (string, bool) {
	switch characterType {
	case CharacterTypeShelly:
		return "shelly", true
	case CharacterTypeColt:
		return "colt", true
	case CharacterTypeLily:
		return "lily", true
	default:
		return "", false
	}
}

type PlayerTypeConfig struct {
	CharacterType CharacterType      `json:"characterType"`
	ID            string             `json:"id"`
	Radius        float64            `json:"radius"`
	HP            float64            `json:"hp"`
	Speed         float64            `json:"speed"`
	NormalAttack  NormalAttackConfig `json:"normalAttack"`
}

type NormalAttackKind string

const (
	NormalAttackSpreadProjectile NormalAttackKind = "spread_projectile"
	NormalAttackBurstProjectile  NormalAttackKind = "burst_projectile"
	NormalAttackMelee            NormalAttackKind = "melee"
)

type ProjectileAttackConfig struct {
	Type                    ProjectileType `json:"type"`
	Count                   int            `json:"count"`
	DirectionOffsetsDegrees []float64      `json:"directionOffsetsDegrees"`
	IntervalTicks           int            `json:"intervalTicks"`
}

type NormalAttackConfig struct {
	Kind          NormalAttackKind        `json:"kind"`
	DamagePerHit  float64                 `json:"damagePerHit"`
	RangeTiles    float64                 `json:"rangeTiles"`
	MaxCharges    int                     `json:"maxCharges"`
	RechargeTicks int                     `json:"rechargeTicks"`
	Projectile    *ProjectileAttackConfig `json:"projectile,omitempty"`
}

func (config *PlayerTypeConfig) UnmarshalJSON(data []byte) error {
	var wire struct {
		CharacterType json.RawMessage    `json:"characterType"`
		ID            string             `json:"id"`
		Radius        float64            `json:"radius"`
		HP            float64            `json:"hp"`
		Speed         float64            `json:"speed"`
		NormalAttack  NormalAttackConfig `json:"normalAttack"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if len(wire.CharacterType) == 0 || bytes.Equal(bytes.TrimSpace(wire.CharacterType), []byte("null")) {
		return fmt.Errorf("game config player type characterType must be present")
	}
	var characterType CharacterType
	if err := json.Unmarshal(wire.CharacterType, &characterType); err != nil {
		return fmt.Errorf("decode game config player type characterType: %w", err)
	}
	*config = PlayerTypeConfig{
		CharacterType: characterType,
		ID:            wire.ID,
		Radius:        wire.Radius,
		HP:            wire.HP,
		Speed:         wire.Speed,
		NormalAttack:  wire.NormalAttack,
	}
	return nil
}

type ProjectileTypeSetConfig struct {
	Types []ProjectileTypeConfig `json:"types"`
}

type ProjectileTypeConfig struct {
	ID     string  `json:"id"`
	Radius float64 `json:"radius"`
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
	if config.Version != ServerGameConfigVersion {
		return GameConfig{}, fmt.Errorf("game config version must be %d", ServerGameConfigVersion)
	}
	if config.TickRate <= 0 {
		return GameConfig{}, fmt.Errorf("game config tickRate must be positive")
	}
	if config.Tile.Size <= 0 {
		return GameConfig{}, fmt.Errorf("game config tile.size must be positive")
	}
	if len(config.Projectile.Types) == 0 {
		return GameConfig{}, fmt.Errorf("game config projectile.types must not be empty")
	}
	seenProjectileIDs := make(map[ProjectileType]bool, len(config.Projectile.Types))
	for _, projectile := range config.Projectile.Types {
		if projectile.ID == "" {
			return GameConfig{}, fmt.Errorf("game config projectile type id must not be empty")
		}
		projectileType := ProjectileType(projectile.ID)
		if seenProjectileIDs[projectileType] {
			return GameConfig{}, fmt.Errorf("game config projectile type id %q must not be duplicated", projectile.ID)
		}
		seenProjectileIDs[projectileType] = true
		if !isFinitePositive(projectile.Radius) || !isFinitePositive(projectile.Speed) {
			return GameConfig{}, fmt.Errorf("game config projectile type %q values must be positive", projectile.ID)
		}
	}
	if err := validatePlayerTypeCatalog(config); err != nil {
		return GameConfig{}, err
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

func validatePlayerTypeCatalog(config GameConfig) error {
	playerTypes := config.Player.Types
	if len(playerTypes) == 0 {
		return fmt.Errorf("game config player.types must not be empty")
	}

	seenTypes := make(map[CharacterType]bool, len(playerTypes))
	seenIDs := make(map[string]bool, len(playerTypes))
	for _, playerType := range playerTypes {
		if seenTypes[playerType.CharacterType] {
			return fmt.Errorf("game config character type %d must not be duplicated", playerType.CharacterType)
		}
		seenTypes[playerType.CharacterType] = true
		if playerType.ID == "" {
			return fmt.Errorf("game config player type id must not be empty")
		}
		if seenIDs[playerType.ID] {
			return fmt.Errorf("game config player type id %q must not be duplicated", playerType.ID)
		}
		seenIDs[playerType.ID] = true
		expectedID, ok := expectedCharacterID(playerType.CharacterType)
		if !ok {
			return fmt.Errorf("game config character type %d is not supported", playerType.CharacterType)
		}
		if playerType.ID != expectedID {
			return fmt.Errorf("game config character type %d must use player type id %q", playerType.CharacterType, expectedID)
		}
		if !isFinitePositive(playerType.Radius) || !isFinitePositive(playerType.HP) || !isFinitePositive(playerType.Speed) {
			return fmt.Errorf("game config player type %q values must be positive", playerType.ID)
		}
		if err := validateNormalAttackConfig(playerType.ID, playerType.NormalAttack, config); err != nil {
			return err
		}
	}

	for _, characterType := range []CharacterType{CharacterTypeShelly, CharacterTypeColt, CharacterTypeLily} {
		if !seenTypes[characterType] {
			return fmt.Errorf("game config character type %d must be present", characterType)
		}
	}
	return nil
}

func validateNormalAttackConfig(playerTypeID string, attack NormalAttackConfig, config GameConfig) error {
	if !isFinitePositive(attack.DamagePerHit) {
		return fmt.Errorf("game config player type %q normalAttack.damagePerHit must be positive", playerTypeID)
	}
	if !isFinitePositive(attack.RangeTiles) {
		return fmt.Errorf("game config player type %q normalAttack.rangeTiles must be positive", playerTypeID)
	}
	if attack.MaxCharges <= 0 {
		return fmt.Errorf("game config player type %q normalAttack.maxCharges must be positive", playerTypeID)
	}
	if attack.RechargeTicks <= 0 {
		return fmt.Errorf("game config player type %q normalAttack.rechargeTicks must be positive", playerTypeID)
	}
	switch attack.Kind {
	case NormalAttackSpreadProjectile:
		if attack.Projectile == nil {
			return fmt.Errorf("game config player type %q normalAttack.projectile is required", playerTypeID)
		}
		if attack.Projectile.Count != 5 || len(attack.Projectile.DirectionOffsetsDegrees) != 5 || attack.Projectile.IntervalTicks != 0 {
			return fmt.Errorf("game config player type %q spread projectile shape is invalid", playerTypeID)
		}
	case NormalAttackBurstProjectile:
		if attack.Projectile == nil {
			return fmt.Errorf("game config player type %q normalAttack.projectile is required", playerTypeID)
		}
		if attack.Projectile.Count != 6 || len(attack.Projectile.DirectionOffsetsDegrees) != 1 || attack.Projectile.DirectionOffsetsDegrees[0] != 0 || attack.Projectile.IntervalTicks <= 0 {
			return fmt.Errorf("game config player type %q burst projectile shape is invalid", playerTypeID)
		}
	case NormalAttackMelee:
		if attack.Projectile != nil {
			return fmt.Errorf("game config player type %q melee normalAttack must not have a projectile", playerTypeID)
		}
		return nil
	default:
		return fmt.Errorf("game config player type %q normalAttack.kind %q is not supported", playerTypeID, attack.Kind)
	}
	if _, ok := config.ProjectileType(attack.Projectile.Type); !ok {
		return fmt.Errorf("game config player type %q normalAttack projectile type %q is not defined", playerTypeID, attack.Projectile.Type)
	}
	return nil
}

func isFinitePositive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
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
	switch mode.Rules.TeamBehavior {
	case TeamBehaviorFreeForAll, TeamBehaviorTwoTeams:
	default:
		return fmt.Errorf("game config mode.rules.teamBehavior %q is not supported", mode.Rules.TeamBehavior)
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
		Version:  ServerGameConfigVersion,
		TickRate: TickRate,
		Tile: TileConfig{
			Size: TileSize,
		},
		Player: PlayerTypeSetConfig{
			Types: []PlayerTypeConfig{
				{
					CharacterType: CharacterTypeShelly,
					ID:            "shelly",
					Radius:        DefaultPlayerRadius,
					HP:            DefaultPlayerHP,
					Speed:         DefaultPlayerSpeed,
					NormalAttack: NormalAttackConfig{
						Kind:          NormalAttackSpreadProjectile,
						DamagePerHit:  280,
						RangeTiles:    7.2,
						MaxCharges:    3,
						RechargeTicks: 30,
						Projectile: &ProjectileAttackConfig{
							Type:                    "default",
							Count:                   5,
							DirectionOffsetsDegrees: []float64{-12, -6, 0, 6, 12},
						},
					},
				},
				{
					CharacterType: CharacterTypeColt,
					ID:            "colt",
					Radius:        DefaultPlayerRadius,
					HP:            3100,
					Speed:         DefaultPlayerSpeed,
					NormalAttack: NormalAttackConfig{
						Kind:          NormalAttackBurstProjectile,
						DamagePerHit:  340,
						RangeTiles:    9,
						MaxCharges:    3,
						RechargeTicks: 30,
						Projectile: &ProjectileAttackConfig{
							Type:                    "default",
							Count:                   6,
							DirectionOffsetsDegrees: []float64{0},
							IntervalTicks:           6,
						},
					},
				},
				{
					CharacterType: CharacterTypeLily,
					ID:            "lily",
					Radius:        DefaultPlayerRadius,
					HP:            4100,
					Speed:         DefaultPlayerSpeed,
					NormalAttack: NormalAttackConfig{
						Kind:          NormalAttackMelee,
						DamagePerHit:  1100,
						RangeTiles:    2.2,
						MaxCharges:    2,
						RechargeTicks: 30,
					},
				},
			},
		},
		Projectile: ProjectileTypeSetConfig{
			Types: []ProjectileTypeConfig{
				{
					ID:     "default",
					Radius: DefaultProjectileRadius,
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
	if playerType, ok := config.PlayerType(CharacterTypeShelly); ok {
		return playerType
	}
	playerType, _ := StaticGameConfig().PlayerType(CharacterTypeShelly)
	return playerType
}

func (config GameConfig) PlayerType(characterType CharacterType) (PlayerTypeConfig, bool) {
	for _, playerType := range config.Player.Types {
		if playerType.CharacterType == characterType {
			return playerType, true
		}
	}
	return PlayerTypeConfig{}, false
}

func (config GameConfig) ProjectileType(projectileType ProjectileType) (ProjectileTypeConfig, bool) {
	for _, candidate := range config.Projectile.Types {
		if ProjectileType(candidate.ID) == projectileType {
			return candidate, true
		}
	}
	return ProjectileTypeConfig{}, false
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
	if gameConfig.Version != ServerGameConfigVersion {
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
