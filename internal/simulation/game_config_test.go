package simulation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

	if config.Version != 6 {
		t.Fatalf("expected server config version 6, got %d", config.Version)
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
	if len(config.Projectile.Types) != 3 {
		t.Fatalf("expected three projectile types, got %+v", config.Projectile.Types)
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
	wantBot := BotConfig{15, 0.25, 0.2, 6, 8, 0.35}
	if config.Bot != wantBot {
		t.Fatalf("bot config=%+v want=%+v", config.Bot, wantBot)
	}
}

func TestLoadServerGameConfigIncludesCanonicalSkillEffects(t *testing.T) {
	config := loadServerGameConfig(t)
	wants := map[CharacterType]SkillConfig{
		CharacterTypeShelly: {Kind: SkillReloadDash, CooldownTicks: 360, ReloadDash: &ReloadDashSkillConfig{DashDistanceTiles: 5.4}},
		CharacterTypeColt: {
			Kind: SkillBurstProjectile, CooldownTicks: 390,
			BurstProjectile: &BurstProjectileSkillConfig{DamagePerHit: 320, RangeTiles: 11, Projectile: ProjectileAttackConfig{
				Type: "colt_skill", Count: 12, DirectionOffsetsDegrees: []float64{0}, EmissionOffsetsTicks: []int{0, 2, 4, 6, 7, 9, 11, 13, 14, 16, 18, 20},
			}},
		},
		CharacterTypeLily: {
			Kind: SkillTeleportProjectile, CooldownTicks: 330,
			TeleportProjectile: &TeleportProjectileSkillConfig{DamagePerHit: 400, RangeTiles: 10.4, BehindDistanceTiles: 1, Projectile: ProjectileAttackConfig{Type: "lily_seed"}},
		},
	}
	for characterType, want := range wants {
		playerType, ok := config.PlayerType(characterType)
		if !ok || !reflect.DeepEqual(playerType.Skill, want) {
			t.Fatalf("character type %d skill=%#v, want %#v", characterType, playerType.Skill, want)
		}
	}

	shelly, _ := config.PlayerType(CharacterTypeShelly)
	if shelly.Skill.Kind != SkillReloadDash || shelly.Skill.ReloadDash == nil || shelly.Skill.ReloadDash.DashDistanceTiles != 5.4 {
		t.Fatalf("Shelly skill=%+v, want reload_dash 5.4 tiles", shelly.Skill)
	}
	colt, _ := config.PlayerType(CharacterTypeColt)
	if colt.Skill.Kind != SkillBurstProjectile || colt.Skill.BurstProjectile == nil {
		t.Fatalf("Colt skill=%+v, want burst_projectile", colt.Skill)
	}
	if got, want := colt.NormalAttack.Projectile.EmissionOffsetsTicks, []int{0, 3, 6, 9, 12, 15}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Colt normal offsets=%v, want %v", got, want)
	}
	if got, want := colt.Skill.BurstProjectile.Projectile.EmissionOffsetsTicks, []int{0, 2, 4, 6, 7, 9, 11, 13, 14, 16, 18, 20}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Colt skill offsets=%v, want %v", got, want)
	}
	if skill := colt.Skill.BurstProjectile; skill.DamagePerHit != 320 || skill.RangeTiles != 11 || skill.Projectile.Type != "colt_skill" || skill.Projectile.Count != 12 {
		t.Fatalf("Colt skill=%+v, want damage/range/type/count 320/11/colt_skill/12", skill)
	}
	coltSkillProjectile, ok := config.ProjectileType("colt_skill")
	if !ok || coltSkillProjectile.Speed != 13 || coltSkillProjectile.Radius != 0.3 {
		t.Fatalf("Colt skill projectile=%+v found=%t, want speed/radius 13/0.3", coltSkillProjectile, ok)
	}
	lily, _ := config.PlayerType(CharacterTypeLily)
	if lily.Skill.Kind != SkillTeleportProjectile || lily.Skill.TeleportProjectile == nil {
		t.Fatalf("Lily skill=%+v, want teleport_projectile", lily.Skill)
	}
	if skill := lily.Skill.TeleportProjectile; skill.DamagePerHit != 400 || skill.RangeTiles != 10.4 || skill.BehindDistanceTiles != 1 || skill.Projectile.Type != "lily_seed" {
		t.Fatalf("Lily skill=%+v, want damage/range/behind/type 400/10.4/1/lily_seed", skill)
	}
	lilySeedProjectile, ok := config.ProjectileType("lily_seed")
	if !ok || lilySeedProjectile.Speed != 13 || lilySeedProjectile.Radius != 0.3 {
		t.Fatalf("Lily seed projectile=%+v found=%t, want speed/radius 13/0.3", lilySeedProjectile, ok)
	}
	for _, id := range []ProjectileType{"default", "colt_skill", "lily_seed"} {
		if _, ok := config.ProjectileType(id); !ok {
			t.Fatalf("missing projectile type %q", id)
		}
	}
}

func TestResolveGameConfigRejectsInvalidBotConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*GameConfig)
	}{
		{"zero detection range", func(c *GameConfig) { c.Bot.DetectionRangeWorld = 0 }},
		{"negative detection range", func(c *GameConfig) { c.Bot.DetectionRangeWorld = -1 }},
		{"nan detection range", func(c *GameConfig) { c.Bot.DetectionRangeWorld = math.NaN() }},
		{"positive infinite detection range", func(c *GameConfig) { c.Bot.DetectionRangeWorld = math.Inf(1) }},
		{"negative infinite detection range", func(c *GameConfig) { c.Bot.DetectionRangeWorld = math.Inf(-1) }},
		{"zero explore arrival distance", func(c *GameConfig) { c.Bot.ExploreArrivalDistanceWorld = 0 }},
		{"negative explore arrival distance", func(c *GameConfig) { c.Bot.ExploreArrivalDistanceWorld = -1 }},
		{"nan explore arrival distance", func(c *GameConfig) { c.Bot.ExploreArrivalDistanceWorld = math.NaN() }},
		{"positive infinite explore arrival distance", func(c *GameConfig) { c.Bot.ExploreArrivalDistanceWorld = math.Inf(1) }},
		{"negative infinite explore arrival distance", func(c *GameConfig) { c.Bot.ExploreArrivalDistanceWorld = math.Inf(-1) }},
		{"zero retreat distance", func(c *GameConfig) { c.Bot.RetreatDistanceWorld = 0 }},
		{"negative retreat distance", func(c *GameConfig) { c.Bot.RetreatDistanceWorld = -1 }},
		{"nan retreat distance", func(c *GameConfig) { c.Bot.RetreatDistanceWorld = math.NaN() }},
		{"positive infinite retreat distance", func(c *GameConfig) { c.Bot.RetreatDistanceWorld = math.Inf(1) }},
		{"negative infinite retreat distance", func(c *GameConfig) { c.Bot.RetreatDistanceWorld = math.Inf(-1) }},
		{"zero projectile look-ahead", func(c *GameConfig) { c.Bot.ProjectileLookAheadWorld = 0 }},
		{"negative projectile look-ahead", func(c *GameConfig) { c.Bot.ProjectileLookAheadWorld = -1 }},
		{"nan projectile look-ahead", func(c *GameConfig) { c.Bot.ProjectileLookAheadWorld = math.NaN() }},
		{"positive infinite projectile look-ahead", func(c *GameConfig) { c.Bot.ProjectileLookAheadWorld = math.Inf(1) }},
		{"negative infinite projectile look-ahead", func(c *GameConfig) { c.Bot.ProjectileLookAheadWorld = math.Inf(-1) }},
		{"zero dodge margin", func(c *GameConfig) { c.Bot.DodgeMarginWorld = 0 }},
		{"negative dodge margin", func(c *GameConfig) { c.Bot.DodgeMarginWorld = -1 }},
		{"nan dodge margin", func(c *GameConfig) { c.Bot.DodgeMarginWorld = math.NaN() }},
		{"positive infinite dodge margin", func(c *GameConfig) { c.Bot.DodgeMarginWorld = math.Inf(1) }},
		{"negative infinite dodge margin", func(c *GameConfig) { c.Bot.DodgeMarginWorld = math.Inf(-1) }},
		{"zero retreat HP ratio", func(c *GameConfig) { c.Bot.RetreatHPRatio = 0 }},
		{"negative retreat HP ratio", func(c *GameConfig) { c.Bot.RetreatHPRatio = -0.1 }},
		{"nan retreat HP ratio", func(c *GameConfig) { c.Bot.RetreatHPRatio = math.NaN() }},
		{"positive infinite retreat HP ratio", func(c *GameConfig) { c.Bot.RetreatHPRatio = math.Inf(1) }},
		{"negative infinite retreat HP ratio", func(c *GameConfig) { c.Bot.RetreatHPRatio = math.Inf(-1) }},
		{"retreat HP ratio above one", func(c *GameConfig) { c.Bot.RetreatHPRatio = 1.01 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := StaticGameConfig()
			tt.mutate(&config)
			if _, err := ResolveGameConfig(config); err == nil {
				t.Fatal("expected invalid bot config to be rejected")
			}
		})
	}
}

func TestLoadServerGameConfigIncludesCharacterNormalAttacks(t *testing.T) {
	config := loadServerGameConfig(t)
	if config.Version != ServerGameConfigVersion {
		t.Fatalf("server version = %d, want %d", config.Version, ServerGameConfigVersion)
	}
	wants := map[CharacterType]NormalAttackConfig{
		CharacterTypeShelly: {Kind: NormalAttackSpreadProjectile, DamagePerHit: 280, RangeTiles: 7.2, MaxCharges: 3, RechargeTicks: 30, Projectile: &ProjectileAttackConfig{Type: "default", Count: 5, DirectionOffsetsDegrees: []float64{-12, -6, 0, 6, 12}}},
		CharacterTypeColt:   {Kind: NormalAttackBurstProjectile, DamagePerHit: 340, RangeTiles: 9, MaxCharges: 3, RechargeTicks: 30, Projectile: &ProjectileAttackConfig{Type: "default", Count: 6, DirectionOffsetsDegrees: []float64{0}, EmissionOffsetsTicks: []int{0, 3, 6, 9, 12, 15}}},
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

func TestLoadServerGameConfigIncludesCharacterSkillCooldowns(t *testing.T) {
	config := loadServerGameConfig(t)
	wants := map[CharacterType]int{
		CharacterTypeShelly: 360,
		CharacterTypeColt:   390,
		CharacterTypeLily:   330,
	}
	for characterType, want := range wants {
		playerType, ok := config.PlayerType(characterType)
		if !ok {
			t.Fatalf("missing character type %d", characterType)
		}
		if got := playerType.Skill.CooldownTicks; got != want {
			t.Fatalf("character type %d cooldown=%d, want %d", characterType, got, want)
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
	if _, err := ResolveGameConfig(config); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("version must be %d", ServerGameConfigVersion)) {
		t.Fatalf("ResolveGameConfig(version 1) error = %v, want exact-version rejection", err)
	}
}

func TestLoadGameConfigRejectsTrailingJSONValuesAndGarbage(t *testing.T) {
	valid, err := json.Marshal(StaticGameConfig())
	if err != nil {
		t.Fatalf("marshal valid config: %v", err)
	}
	for name, suffix := range map[string]string{
		"second JSON value": ` {}`,
		"trailing garbage":  ` garbage`,
	} {
		t.Run(name, func(t *testing.T) {
			config, err := LoadGameConfig(strings.NewReader(string(valid) + suffix))
			if err == nil {
				t.Fatalf("LoadGameConfig() config=%+v error=nil, want trailing input rejection", config)
			}
			if config.Version != 0 {
				t.Fatalf("LoadGameConfig() config=%+v, want zero config on error", config)
			}
		})
	}
}

func TestResolveStateGameConfigPanicsOnExplicitInvalidConfig(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("resolveStateGameConfig() did not panic for explicit invalid config")
		}
	}()
	resolveStateGameConfig(Config{Game: GameConfig{Version: 1}})
}

func TestResolveGameConfigRejectsInvalidSkillCooldown(t *testing.T) {
	for _, cooldown := range []int{0, -1} {
		t.Run(fmt.Sprintf("cooldown_%d", cooldown), func(t *testing.T) {
			config := StaticGameConfig()
			config.Player.Types[0].Skill.CooldownTicks = cooldown
			_, err := ResolveGameConfig(config)
			if err == nil || !strings.Contains(err.Error(), "skill.cooldownTicks must be positive") {
				t.Fatalf("ResolveGameConfig() error=%v", err)
			}
		})
	}
}

func TestResolveGameConfigRejectsInvalidTypedSkillCombinations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*GameConfig)
	}{
		{"unknown kind", func(c *GameConfig) { c.Player.Types[0].Skill.Kind = "unknown" }},
		{"missing reload payload", func(c *GameConfig) { c.Player.Types[0].Skill.ReloadDash = nil }},
		{"reload forbidden burst payload", func(c *GameConfig) { c.Player.Types[0].Skill.BurstProjectile = c.Player.Types[1].Skill.BurstProjectile }},
		{"zero dash", func(c *GameConfig) { c.Player.Types[0].Skill.ReloadDash.DashDistanceTiles = 0 }},
		{"noncanonical dash", func(c *GameConfig) { c.Player.Types[0].Skill.ReloadDash.DashDistanceTiles = 5.3 }},
		{"wrong character kind", func(c *GameConfig) { c.Player.Types[0].Skill = c.Player.Types[1].Skill }},
		{"zero burst damage", func(c *GameConfig) { c.Player.Types[1].Skill.BurstProjectile.DamagePerHit = 0 }},
		{"zero burst range", func(c *GameConfig) { c.Player.Types[1].Skill.BurstProjectile.RangeTiles = 0 }},
		{"missing burst offsets", func(c *GameConfig) { c.Player.Types[1].Skill.BurstProjectile.Projectile.EmissionOffsetsTicks = nil }},
		{"burst count mismatch", func(c *GameConfig) { c.Player.Types[1].Skill.BurstProjectile.Projectile.Count = 11 }},
		{"burst mixed interval", func(c *GameConfig) { c.Player.Types[1].Skill.BurstProjectile.Projectile.IntervalTicks = 1 }},
		{"burst duplicate offset", func(c *GameConfig) { c.Player.Types[1].Skill.BurstProjectile.Projectile.EmissionOffsetsTicks[2] = 2 }},
		{"burst noncanonical increasing offsets", func(c *GameConfig) {
			c.Player.Types[1].Skill.BurstProjectile.Projectile.EmissionOffsetsTicks = []int{0, 2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 22}
		}},
		{"burst direction offset", func(c *GameConfig) { c.Player.Types[1].Skill.BurstProjectile.Projectile.DirectionOffsetsDegrees[0] = 1 }},
		{"burst unknown projectile", func(c *GameConfig) { c.Player.Types[1].Skill.BurstProjectile.Projectile.Type = "missing" }},
		{"zero teleport damage", func(c *GameConfig) { c.Player.Types[2].Skill.TeleportProjectile.DamagePerHit = 0 }},
		{"zero teleport range", func(c *GameConfig) { c.Player.Types[2].Skill.TeleportProjectile.RangeTiles = 0 }},
		{"noncanonical teleport range", func(c *GameConfig) { c.Player.Types[2].Skill.TeleportProjectile.RangeTiles = 10.3 }},
		{"zero behind distance", func(c *GameConfig) { c.Player.Types[2].Skill.TeleportProjectile.BehindDistanceTiles = 0 }},
		{"teleport unknown projectile", func(c *GameConfig) { c.Player.Types[2].Skill.TeleportProjectile.Projectile.Type = "missing" }},
		{"teleport forbidden schedule", func(c *GameConfig) { c.Player.Types[2].Skill.TeleportProjectile.Projectile.Count = 1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := StaticGameConfig()
			tt.mutate(&config)
			if _, err := ResolveGameConfig(config); err == nil {
				t.Fatal("expected invalid typed skill config to be rejected")
			}
		})
	}
}

func TestSkillConfigJSONRejectsUnknownKindAndKindSpecificFieldMixes(t *testing.T) {
	tests := map[string]string{
		"unknown kind":                     `{"kind":"unknown","cooldownTicks":1}`,
		"reload forbidden damage":          `{"kind":"reload_dash","cooldownTicks":1,"dashDistanceTiles":5.4,"damagePerHit":1}`,
		"reload forbidden damage null":     `{"kind":"reload_dash","cooldownTicks":1,"dashDistanceTiles":5.4,"damagePerHit":null}`,
		"reload forbidden projectile null": `{"kind":"reload_dash","cooldownTicks":1,"dashDistanceTiles":5.4,"projectile":null}`,
		"burst missing damage":             `{"kind":"burst_projectile","cooldownTicks":1,"rangeTiles":11,"projectile":{"type":"colt_skill"}}`,
		"burst forbidden dash null":        `{"kind":"burst_projectile","cooldownTicks":1,"damagePerHit":320,"rangeTiles":11,"dashDistanceTiles":null,"projectile":{"type":"colt_skill"}}`,
		"teleport forbidden dash":          `{"kind":"teleport_projectile","cooldownTicks":1,"damagePerHit":400,"rangeTiles":10.4,"behindDistanceTiles":1,"dashDistanceTiles":5.4,"projectile":{"type":"lily_seed"}}`,
		"teleport forbidden dash null":     `{"kind":"teleport_projectile","cooldownTicks":1,"damagePerHit":400,"rangeTiles":10.4,"behindDistanceTiles":1,"dashDistanceTiles":null,"projectile":{"type":"lily_seed"}}`,
		"unknown field":                    `{"kind":"reload_dash","cooldownTicks":1,"dashDistanceTiles":5.4,"script":"unsafe"}`,
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			var skill SkillConfig
			if err := json.Unmarshal([]byte(payload), &skill); err == nil {
				t.Fatalf("json.Unmarshal(%s) skill=%+v error=nil, want rejection", payload, skill)
			}
		})
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
		{"spread emission offsets", func(c *GameConfig) {
			c.Player.Types[0].NormalAttack.Projectile.EmissionOffsetsTicks = []int{0, 1, 2, 3, 4}
		}},
		{"burst count", func(c *GameConfig) { c.Player.Types[1].NormalAttack.Projectile.Count = 1 }},
		{"burst offset", func(c *GameConfig) { c.Player.Types[1].NormalAttack.Projectile.DirectionOffsetsDegrees = []float64{1} }},
		{"burst nan offset", func(c *GameConfig) {
			c.Player.Types[1].NormalAttack.Projectile.DirectionOffsetsDegrees = []float64{math.NaN()}
		}},
		{"burst infinite offset", func(c *GameConfig) {
			c.Player.Types[1].NormalAttack.Projectile.DirectionOffsetsDegrees = []float64{math.Inf(1)}
		}},
		{"burst interval mixed with offsets", func(c *GameConfig) { c.Player.Types[1].NormalAttack.Projectile.IntervalTicks = 1 }},
		{"burst offset count mismatch", func(c *GameConfig) { c.Player.Types[1].NormalAttack.Projectile.Count = 7 }},
		{"burst offset does not start at zero", func(c *GameConfig) { c.Player.Types[1].NormalAttack.Projectile.EmissionOffsetsTicks[0] = 1 }},
		{"burst offset duplicate", func(c *GameConfig) { c.Player.Types[1].NormalAttack.Projectile.EmissionOffsetsTicks[2] = 3 }},
		{"burst noncanonical increasing offsets", func(c *GameConfig) {
			c.Player.Types[1].NormalAttack.Projectile.EmissionOffsetsTicks = []int{0, 4, 8, 12, 16, 20}
		}},
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

func TestResolveGameConfigRejectsCanonicalSpawnOverlapUsingMaximumCharacterRadius(t *testing.T) {
	config := StaticGameConfig()
	config.Player.Types[2].Radius = 0.75
	config.ModeCatalog.Catalog = []GameModeConfig{DefaultGameModeConfig()}
	config.Map = canonicalSpawnValidationMap()

	_, err := ResolveGameConfig(config)
	if err == nil {
		t.Fatal("expected canonical spawn circles to be rejected")
	}
	if !strings.Contains(err.Error(), "spawn") || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("ResolveGameConfig() error=%v, want spawn-overlap rejection", err)
	}
}

func TestResolveGameConfigAcceptsCanonicalSpawnsWhenMaximumRadiusFits(t *testing.T) {
	config := StaticGameConfig()
	config.Player.Types[2].Radius = 0.6
	config.ModeCatalog.Catalog = []GameModeConfig{DefaultGameModeConfig()}
	config.Map = canonicalSpawnValidationMap()

	if _, err := ResolveGameConfig(config); err != nil {
		t.Fatalf("ResolveGameConfig() error=%v, want non-overlapping canonical spawns", err)
	}
}

func TestResolveGameConfigAcceptsTangentCanonicalSpawnCircles(t *testing.T) {
	config := StaticGameConfig()
	config.ModeCatalog.Catalog = []GameModeConfig{DefaultGameModeConfig()}
	config.Map = MapData{
		Width:      4,
		Height:     4,
		MaxPlayers: 2,
		TileSize:   1,
		Map: [][]TileType{
			{TileWall, TileWall, TileWall, TileWall},
			{TileWall, TileSpawnPoint, TileSpawnPoint, TileWall},
			{TileWall, TileGround, TileGround, TileWall},
			{TileWall, TileWall, TileWall, TileWall},
		},
	}

	if _, err := ResolveGameConfig(config); err != nil {
		t.Fatalf("ResolveGameConfig() error=%v, want tangent canonical spawns accepted", err)
	}
}

func canonicalSpawnValidationMap() MapData {
	return MapData{
		Width:      4,
		Height:     4,
		MaxPlayers: 2,
		TileSize:   1,
		Map: [][]TileType{
			{TileWall, TileWall, TileWall, TileWall},
			{TileWall, TileSpawnPoint, TileGround, TileWall},
			{TileWall, TileGround, TileGround, TileWall},
			{TileWall, TileWall, TileWall, TileWall},
		},
	}
}

func TestServerGameConfigArtifactIncludesRuntimeMap(t *testing.T) {
	config := loadServerGameConfig(t)
	gameMap, err := ResolveMapData(config.Map)
	if err != nil {
		t.Fatalf("resolve server game config map: %v", err)
	}

	if gameMap.Width != 40 || gameMap.Height != 40 {
		t.Fatalf("expected 40x40 runtime map, got %dx%d", gameMap.Width, gameMap.Height)
	}
	if gameMap.Index != 0 {
		t.Fatalf("expected runtime map index 0, got %d", gameMap.Index)
	}
	if gameMap.MaxPlayers != 6 {
		t.Fatalf("expected map maxPlayers 6, got %d", gameMap.MaxPlayers)
	}
	if gameMap.TileSize != TileSize {
		t.Fatalf("expected map tile size %f, got %f", TileSize, gameMap.TileSize)
	}
	if got := countMapTile(gameMap, TileSpawnPoint); got != 6 {
		t.Fatalf("expected exactly six spawn tiles, got %d", got)
	}
}

func TestServerGameConfigArtifactMatchesClientMap0(t *testing.T) {
	config := loadServerGameConfig(t)
	want := expectedClientMap0()
	if config.Map.Width != len(want[0]) || config.Map.Height != len(want) {
		t.Fatalf("server runtime map metadata drifted from approved Map_0: got %dx%d want %dx%d", config.Map.Width, config.Map.Height, len(want[0]), len(want))
	}
	if !reflect.DeepEqual(config.Map.Map, want) {
		t.Fatalf("server runtime map drifted from SL-111 client Map_0:\n got: %+v\nwant: %+v", config.Map.Map, want)
	}
	if got := countMapTile(config.Map, TileSpawnPoint); got != 6 {
		t.Fatalf("server runtime map has %d spawn tiles, want exactly 6", got)
	}
}

const (
	clientMap0SourceMainSHA          = "50f10c27a575c2bc8f53c7e7b3385de69876184c"
	clientMap0SourceLastChangeCommit = "4f3292603e6809e918f609e5be8dd03d3ded8988"
	clientMap0SourceBlobSHA          = "89228cead52df257a0489101d045b3d288634e27"
	clientMap0SourceRawSHA256        = "babb748ff60827499992d7020ec296bc72afa32928ecf5642b3c4e82d943cf00"
	clientMap0SemanticSHA256         = "b1729488ec19efb433d19df112b88f1fd1b33a1f39f15fb1cb4df0f93d9f8e60"
)

func TestServerGameConfigArtifactPinsApprovedClientMap0Source(t *testing.T) {
	config := loadServerGameConfig(t)
	if got := semanticMap0SHA256(config.Map); got != clientMap0SemanticSHA256 {
		t.Fatalf("server runtime Map_0 semantic SHA256=%s, want approved Client Map_0 %s", got, clientMap0SemanticSHA256)
	}
	t.Logf("approved Client Map_0 source: main=%s last-change=%s blob=%s raw-sha256=%s semantic-sha256=%s", clientMap0SourceMainSHA, clientMap0SourceLastChangeCommit, clientMap0SourceBlobSHA, clientMap0SourceRawSHA256, clientMap0SemanticSHA256)
}

func semanticMap0SHA256(gameMap MapData) string {
	// This field order is the jq -S canonical order (including its trailing newline).
	mapRows := make([][]int, len(gameMap.Map))
	for y, row := range gameMap.Map {
		mapRows[y] = make([]int, len(row))
		for x, tile := range row {
			mapRows[y][x] = int(tile)
		}
	}
	canonical := struct {
		Height     int     `json:"height"`
		Index      int     `json:"index"`
		Map        [][]int `json:"map"`
		MaxPlayers int     `json:"maxPlayers"`
		Width      int     `json:"width"`
	}{
		Height:     gameMap.Height,
		Index:      gameMap.Index,
		Map:        mapRows,
		MaxPlayers: gameMap.MaxPlayers,
		Width:      gameMap.Width,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		panic(fmt.Sprintf("marshal canonical Map_0: %v", err))
	}
	digest := sha256.Sum256(append(encoded, '\n'))
	return hex.EncodeToString(digest[:])
}

func countMapTile(gameMap MapData, want TileType) int {
	count := 0
	for _, row := range gameMap.Map {
		for _, tile := range row {
			if tile == want {
				count++
			}
		}
	}
	return count
}

func expectedClientMap0() [][]TileType {
	return [][]TileType{
		{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
		{1, 0, 0, 0, 0, 0, 0, 0, 3, 3, 3, 3, 3, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3, 3, 3, 3, 3, 3, 3, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 2, 0, 0, 0, 0, 0, 3, 3, 3, 3, 3, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3, 3, 3, 3, 3, 3, 0, 0, 0, 0, 0, 2, 0, 1},
		{1, 0, 0, 0, 0, 0, 1, 1, 1, 3, 3, 3, 3, 3, 0, 0, 0, 4, 4, 4, 0, 0, 0, 0, 3, 3, 3, 3, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 1, 1, 1, 3, 3, 3, 3, 3, 0, 0, 4, 4, 4, 4, 0, 0, 0, 0, 3, 3, 3, 3, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 1, 0, 0, 3, 3, 3, 0, 0, 0, 0, 4, 4, 4, 4, 0, 0, 0, 0, 0, 0, 3, 3, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 1, 1, 0, 0, 3, 3, 3, 0, 0, 0, 0, 4, 4, 4, 4, 0, 0, 0, 0, 0, 0, 3, 3, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 3, 3, 3, 3, 0, 1, 1, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 1, 1, 3, 3, 3, 3, 3, 3, 1},
		{1, 3, 3, 3, 3, 3, 3, 1, 1, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 3, 3, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 1, 1, 3, 3, 3, 3, 3, 3, 1},
		{1, 3, 3, 3, 3, 3, 3, 1, 1, 0, 0, 0, 0, 0, 1, 1, 0, 0, 3, 3, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 1, 1, 3, 3, 3, 3, 3, 3, 1},
		{1, 3, 3, 3, 3, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 3, 3, 3, 3, 1},
		{1, 3, 3, 3, 3, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 3, 3, 3, 3, 1},
		{1, 0, 0, 3, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3, 3, 0, 0, 1},
		{1, 0, 0, 3, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 4, 4, 4, 4, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4, 4, 4, 4, 4, 4, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 0, 4, 4, 4, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4, 4, 4, 4, 4, 4, 4, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 0, 0, 4, 4, 4, 4, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4, 4, 4, 4, 4, 0, 0, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 2, 0, 0, 0, 0, 0, 0, 4, 4, 4, 4, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4, 4, 4, 4, 4, 0, 0, 0, 0, 0, 0, 2, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 3, 3, 3, 3, 3, 3, 3, 0, 0, 0, 0, 0, 0, 0, 4, 4, 4, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3, 3, 3, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 3, 3, 3, 3, 3, 3, 0, 0, 0, 0, 0, 0, 4, 4, 4, 4, 0, 0, 0, 0, 0, 0, 0, 0, 3, 3, 3, 3, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 3, 3, 3, 0, 0, 0, 4, 4, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4, 4, 0, 1},
		{1, 0, 0, 0, 0, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 0, 0, 0, 0, 4, 4, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 0, 0, 0, 0, 4, 4, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 4, 4, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 1, 1, 1, 0, 0, 4, 4, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 1, 1, 1, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4, 4, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4, 4, 4, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 3, 3, 3, 3, 3, 3, 3, 0, 0, 0, 0, 4, 4, 4, 4, 4, 0, 0, 0, 3, 3, 3, 3, 3, 3, 3, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 3, 3, 3, 3, 3, 3, 3, 0, 0, 0, 0, 4, 4, 4, 4, 4, 0, 0, 0, 3, 3, 3, 3, 3, 3, 3, 0, 0, 0, 0, 0, 0, 1},
		{1, 0, 2, 0, 0, 0, 0, 3, 3, 3, 3, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3, 3, 3, 3, 3, 0, 0, 0, 0, 2, 0, 1},
		{1, 0, 0, 0, 0, 0, 0, 0, 3, 3, 3, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3, 3, 3, 3, 0, 0, 0, 0, 0, 0, 1},
		{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
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
