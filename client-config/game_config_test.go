package clientconfig

import (
	"encoding/json"
	"io"
	"reflect"
	"testing"
)

const canonicalV3Fixture = `{
  "version": 3,
  "tileSize": 1.2,
  "playerRadius": 0.5,
  "characters": [
    {"type": 0, "normalAttackDistance": 5.0, "skillAttackDistance": 1.0, "skillAttackCoolDown": 10, "maxBullets": 3},
    {"type": 1, "normalAttackDistance": 1.5, "skillAttackDistance": 3.0, "skillAttackCoolDown": 10, "maxBullets": 3},
    {"type": 2, "normalAttackDistance": 6.0, "skillAttackDistance": 7.0, "skillAttackCoolDown": 10, "maxBullets": 4}
  ],
  "normalAttackCoolDown": 1,
  "projectileRadius": 0.3
}`

func TestParseEmbeddedGameConfigV3(t *testing.T) {
	data, err := io.ReadAll(Reader())
	if err != nil {
		t.Fatalf("read embedded game config: %v", err)
	}

	config, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if config.Version != Version {
		t.Fatalf("version = %d, want %d", config.Version, Version)
	}
	if config.TileSize != 1.2 || config.PlayerRadius != 0.5 ||
		config.NormalAttackCoolDown != 1 || config.ProjectileRadius != 0.3 {
		t.Fatalf("shared config values = %+v", config)
	}

	assertCharacter(t, config, CharacterConfig{
		Type: 0, NormalAttackDistance: 5, SkillAttackDistance: 1,
		SkillAttackCoolDown: 10, MaxBullets: 3,
	})
	assertCharacter(t, config, CharacterConfig{
		Type: 1, NormalAttackDistance: 1.5, SkillAttackDistance: 3,
		SkillAttackCoolDown: 10, MaxBullets: 3,
	})
	assertCharacter(t, config, CharacterConfig{
		Type: 2, NormalAttackDistance: 6, SkillAttackDistance: 7,
		SkillAttackCoolDown: 10, MaxBullets: 4,
	})
}

func TestEmbeddedGameConfigUsesCanonicalSchemaKeys(t *testing.T) {
	data, err := io.ReadAll(Reader())
	if err != nil {
		t.Fatalf("read embedded game config: %v", err)
	}

	var topLevel map[string]json.RawMessage
	if err := json.Unmarshal(data, &topLevel); err != nil {
		t.Fatalf("decode top-level keys: %v", err)
	}
	assertExactKeys(t, topLevel, []string{
		"version", "tileSize", "playerRadius", "characters",
		"normalAttackCoolDown", "projectileRadius",
	})

	var characters []map[string]json.RawMessage
	if err := json.Unmarshal(topLevel["characters"], &characters); err != nil {
		t.Fatalf("decode character keys: %v", err)
	}
	for _, character := range characters {
		assertExactKeys(t, character, []string{
			"type", "normalAttackDistance", "skillAttackDistance",
			"skillAttackCoolDown", "maxBullets",
		})
	}
}

func TestParseRejectsInvalidClientContract(t *testing.T) {
	tests := map[string]func(map[string]any){
		"old version": func(config map[string]any) {
			config["version"] = float64(2)
		},
		"missing top level field": func(config map[string]any) {
			delete(config, "tileSize")
		},
		"null top level field": func(config map[string]any) {
			config["playerRadius"] = nil
		},
		"missing character field": func(config map[string]any) {
			delete(characterAt(t, config, 0), "normalAttackDistance")
		},
		"zero character cooldown": func(config map[string]any) {
			characterAt(t, config, 0)["skillAttackCoolDown"] = float64(0)
		},
		"negative max bullets": func(config map[string]any) {
			characterAt(t, config, 1)["maxBullets"] = float64(-1)
		},
		"duplicate character type": func(config map[string]any) {
			characterAt(t, config, 2)["type"] = float64(1)
		},
		"unsupported character type": func(config map[string]any) {
			characterAt(t, config, 2)["type"] = float64(3)
		},
		"missing character": func(config map[string]any) {
			config["characters"] = config["characters"].([]any)[:2]
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			payload := mutatedCanonicalFixture(t, mutate)
			if _, err := Parse(payload); err == nil {
				t.Fatal("Parse() error = nil")
			}
		})
	}
}

func TestParseAllowsUnknownAdditiveFields(t *testing.T) {
	payload := mutatedCanonicalFixture(t, func(config map[string]any) {
		config["futureTopLevel"] = true
		characterAt(t, config, 0)["futureCharacterField"] = "value"
	})

	config, err := Parse(payload)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(config.Characters) != 3 {
		t.Fatalf("characters = %+v", config.Characters)
	}
}

func assertCharacter(t *testing.T, config Config, want CharacterConfig) {
	t.Helper()
	for _, character := range config.Characters {
		if character.Type != want.Type {
			continue
		}
		if !reflect.DeepEqual(character, want) {
			t.Fatalf("character type %d = %+v, want %+v", want.Type, character, want)
		}
		return
	}
	t.Fatalf("missing character type %d in %+v", want.Type, config.Characters)
}

func mutatedCanonicalFixture(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	var config map[string]any
	if err := json.Unmarshal([]byte(canonicalV3Fixture), &config); err != nil {
		t.Fatalf("decode canonical fixture: %v", err)
	}
	mutate(config)
	payload, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("encode mutated fixture: %v", err)
	}
	return payload
}

func characterAt(t *testing.T, config map[string]any, index int) map[string]any {
	t.Helper()
	characters, ok := config["characters"].([]any)
	if !ok || index < 0 || index >= len(characters) {
		t.Fatalf("characters fixture = %#v", config["characters"])
	}
	character, ok := characters[index].(map[string]any)
	if !ok {
		t.Fatalf("character fixture = %#v", characters[index])
	}
	return character
}

func assertExactKeys(t *testing.T, object map[string]json.RawMessage, want []string) {
	t.Helper()
	if len(object) != len(want) {
		t.Fatalf("keys = %v, want %v", reflect.ValueOf(object).MapKeys(), want)
	}
	for _, key := range want {
		if _, ok := object[key]; !ok {
			t.Fatalf("missing key %q in %v", key, reflect.ValueOf(object).MapKeys())
		}
	}
}
