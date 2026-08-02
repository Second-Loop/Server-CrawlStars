# SL-99 Client Config Contract Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Server의 client artifact와 Unity parser/build preprocessor를 하나의 검증 가능한 v3 계약으로 맞춰 silent `0`을 제거해요.

**Architecture:** Server-CrawlStars가 v3 JSON과 Go validator를 소유하고, Client-CrawlStars는 같은 field를 typed parse한 뒤 build write와 runtime apply 전에 검증해요. Client 설정값은 UI·로컬 bot용이고 server gameplay 판정은 기존 `server-config`와 snapshot을 계속 최종 truth로 사용해요.

**Tech Stack:** Go 1.25, JSON, C# 9/Unity 6000.3.15f1, Newtonsoft.Json, NUnit EditMode tests

## Global Constraints

- Canonical client artifact version은 `3`이에요.
- Character type은 `0=Shelly`, `1=Colt`, `2=Lily`이고 재번호화하지 않아요.
- Client 값은 댓글에서 승인된 `normalAttackDistance=5/1.5/6`, `skillAttackDistance=1/3/7`, `skillAttackCoolDown=10/10/10`, `maxBullets=3/3/4`를 사용해요.
- `normalAttackCoolDown=1`, `tileSize=1.2`, `playerRadius=0.5`, `projectileRadius=0.3`을 유지해요.
- Server의 authoritative normal attack 거리·charge와 skill tick 판정은 변경하지 않아요.
- 신규 WebSocket field, skill effect, balance 변경을 추가하지 않아요.
- Unknown JSON field는 허용하지만 version, 필수 field, exact character type set, 양수 값은 엄격하게 검증해요.
- 모든 production 변경은 실패하는 test를 먼저 확인한 뒤 구현해요.

---

### Task 1: Server client artifact validator와 v3 fixture

**Files:**
- Modify: `client-config/game_config.go`
- Modify: `client-config/game-config.json`
- Create: `client-config/game_config_test.go`
- Modify: `internal/simulation/game_config.go`
- Modify: `internal/simulation/game_config_test.go`

**Interfaces:**
- Produces: `const clientconfig.Version = 3`
- Produces: `type clientconfig.Config`
- Produces: `type clientconfig.CharacterConfig`
- Produces: `func clientconfig.Parse(data []byte) (Config, error)`
- Preserves: `func clientconfig.Reader() io.Reader`
- Preserves: `server-config/game-config.json`와 simulation runtime behavior

- [ ] **Step 1: v3 artifact와 invalid fixture를 표현하는 실패 test 작성**

`client-config/game_config_test.go`에 embedded artifact의 exact 값을 확인하고 invalid payload를 table test로 거부하는 test를 추가해요.

```go
func TestParseEmbeddedGameConfigV3(t *testing.T) {
	data, err := io.ReadAll(Reader())
	if err != nil {
		t.Fatal(err)
	}
	config, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if config.Version != 3 || len(config.Characters) != 3 {
		t.Fatalf("config = %+v", config)
	}
	assertCharacter(t, config, 0, 5, 1, 10, 3)
	assertCharacter(t, config, 1, 1.5, 3, 10, 3)
	assertCharacter(t, config, 2, 6, 7, 10, 4)
}

func TestParseRejectsInvalidClientContract(t *testing.T) {
	tests := map[string]string{
		"old version":       validClientConfigWith(`"version":2`),
		"missing distance":  validClientConfigWithout("normalAttackDistance"),
		"duplicate type":    validClientConfigWithDuplicateType(),
		"unsupported type":  validClientConfigWithType(3),
		"zero cooldown":     validClientConfigWith(`"normalAttackCoolDown":0`),
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(payload)); err == nil {
				t.Fatal("Parse() error = nil")
			}
		})
	}
}
```

- [ ] **Step 2: RED 확인**

Run:

```bash
rtk go test ./client-config -count=1
```

Expected: `Parse`, `Config`, `CharacterConfig`가 없어 compile failure가 발생해요.

- [ ] **Step 3: 최소 parser와 validator 구현**

`client-config/game_config.go`에서 pointer wire DTO로 missing/null을 구분하고 public value DTO로 변환해요.

```go
const Version = 3

type Config struct {
	Version                int
	TileSize               float64
	PlayerRadius           float64
	Characters             []CharacterConfig
	NormalAttackCoolDown   int
	ProjectileRadius       float64
}

type CharacterConfig struct {
	Type                    int
	NormalAttackDistance    float64
	SkillAttackDistance     float64
	SkillAttackCoolDown     int
	MaxBullets              int
}

func Parse(data []byte) (Config, error) {
	var wire rawConfig
	if err := json.Unmarshal(data, &wire); err != nil {
		return Config{}, fmt.Errorf("decode client game config: %w", err)
	}
	config, err := wire.resolve()
	if err != nil {
		return Config{}, err
	}
	return config, nil
}
```

`rawConfig.resolve`는 다음을 검증해요.

- 모든 pointer가 non-nil
- version이 정확히 `3`
- 공용 float와 cooldown이 finite positive
- character 길이가 정확히 `3`
- type이 중복 없는 exact set `0/1/2`
- 각 distance/cooldown/maxBullets가 positive

- [ ] **Step 4: canonical JSON을 v3로 교체**

설계 문서의 JSON bytes를 `client-config/game-config.json`에 반영하고 v2 전용 `playerTypes`, `projectileTypes`, `characterType/id/name/role`을 제거해요.

- [ ] **Step 5: GREEN 확인**

Run:

```bash
rtk go test ./client-config -count=1
```

Expected: v3 exact fixture와 invalid table이 모두 PASS해요.

- [ ] **Step 6: simulation test의 client DTO를 새 parser로 전환**

`internal/simulation/game_config_test.go`는 직접 JSON DTO를 유지하지 않고 `clientconfig.Parse`를 사용해요. `client.characters[].type`과 server `player.types[].characterType`의 stable numeric set만 비교하고 client ID/name/role 일치 assertion은 제거해요. 사용처가 사라진 `simulation.ClientGameConfigVersion`도 제거해요.

- [ ] **Step 7: Server 범위 회귀 확인**

Run:

```bash
rtk go test ./client-config ./internal/simulation -count=1
rtk git diff --check
```

Expected: 두 package가 PASS하고 whitespace error가 없어요.

- [ ] **Step 8: Server validator commit**

```bash
rtk git add client-config/game_config.go client-config/game-config.json client-config/game_config_test.go internal/simulation/game_config.go internal/simulation/game_config_test.go
rtk git commit -m "[SL-99] feat(config): client artifact v3 계약 검증" -m "- 클라이언트 제안 캐릭터 설정값 반영
- 필수 필드와 버전, 캐릭터 타입 검증 추가"
```

---

### Task 2: Server 문서와 drift validator

**Files:**
- Modify: `docs-ui/scripts/validate.mjs`
- Modify: `ai-docs/architecture.md`
- Modify: `ai-docs/protocol.md`
- Modify: `ai-docs/api-reference.md`
- Modify: `ai-docs/project-map.md`
- Modify: `ai-docs/decisions.md`

**Interfaces:**
- Produces: client artifact v3 field와 ownership을 확인하는 source marker
- Produces: ADR-0037 client-shared config와 server-authoritative gameplay 분리
- Preserves: `api/openapi.yaml`, `api/asyncapi.yaml` wire schema

- [ ] **Step 1: 문서 marker RED 추가**

`docs-ui/scripts/validate.mjs`에 다음 의미 marker를 확인하는 assertion을 먼저 추가해요.

```js
assert(architectureText.includes('client config v3'), 'architecture must document client config v3');
assert(architectureText.includes('normalAttackDistance'), 'architecture must document normalAttackDistance');
assert(architectureText.includes('skillAttackCoolDown'), 'architecture must document skillAttackCoolDown');
assert(architectureText.includes('server-authoritative'), 'architecture must preserve server-authoritative ownership');
```

Run:

```bash
rtk node docs-ui/scripts/validate.mjs
```

Expected: 아직 문서 marker가 없어 FAIL해요.

- [ ] **Step 2: 소유권과 v3 계약 문서화**

관련 문서에서 v2 identity/render catalog 설명을 v3 runtime client config로 바꾸고 다음 경계를 명시해요.

- distance는 Unity world unit
- cooldown은 client UI/로컬 bot이 쓰는 초 단위
- `maxBullets`는 client charge 표현값
- 실제 hit/range/charge/skill 승인 결과는 server config와 snapshot이 최종 truth
- client v3 변경은 REST/WebSocket schema 변경이 아님

`ai-docs/decisions.md`에는 ADR-0037을 추가해 breaking version과 strict validation, cross-repo rollout을 기록해요.

- [ ] **Step 3: 문서 GREEN과 계약 비변경 확인**

Run:

```bash
rtk node docs-ui/scripts/validate.mjs
rtk make docs-build
rtk git diff --exit-code origin/main -- api/openapi.yaml api/asyncapi.yaml
rtk git diff --check
```

Expected: source validation/build가 PASS하고 OpenAPI/AsyncAPI diff가 없어요.

- [ ] **Step 4: Server docs commit**

```bash
rtk git add docs-ui/scripts/validate.mjs ai-docs/architecture.md ai-docs/protocol.md ai-docs/api-reference.md ai-docs/project-map.md ai-docs/decisions.md
rtk git commit -m "[SL-99] docs(config): client v3 소유권 명시" -m "- client UI 값과 server gameplay truth 분리
- config schema drift marker와 ADR 추가"
```

---

### Task 3: Unity runtime parser strict validation

**Files:**
- Modify: `CrawlStars/Assets/Scripts/Core/GameConfig.cs`
- Create: `CrawlStars/Assets/Editor/Tests/Core/GameConfigTests.cs`
- Create: `CrawlStars/Assets/Editor/Tests/Core/GameConfigTests.cs.meta`
- Modify: `CrawlStars/Assets/StreamingAssets/game-config.json`

**Interfaces:**
- Produces: `public static bool GameConfig.TryParse(string json, out ParsedConfig config, out string error)`
- Produces: `public sealed class GameConfig.ParsedConfig`
- Consumes: Server `client-config/game-config.json` v3
- Preserves: `GameConfig.LoadAsync()`의 `UniTask<bool>` signature

- [ ] **Step 1: Client 격리 checkout 준비**

```bash
rtk git clone https://github.com/Second-Loop/Client-CrawlStars /private/tmp/client-crawlstars-sl99
rtk git -C /private/tmp/client-crawlstars-sl99 checkout -b sl-99-client-config-contract origin/main
```

- [ ] **Step 2: parser RED test 작성**

`GameConfigTests.cs`에 실제 server v3 fixture 성공과 invalid table을 추가해요.

```csharp
[Test]
public void TryParse_AcceptsCanonicalV3() {
    bool ok = GameConfig.TryParse(CanonicalV3, out var parsed, out var error);
    Assert.That(ok, Is.True, error);
    Assert.That(parsed.Version, Is.EqualTo(3));
    Assert.That(parsed.Characters.Select(value => value.Type), Is.EqualTo(new[] { 0, 1, 2 }));
}

[TestCase("old-version")]
[TestCase("missing-normal-distance")]
[TestCase("duplicate-type")]
[TestCase("zero-cooldown")]
public void TryParse_RejectsInvalidContract(string fixture) {
    bool ok = GameConfig.TryParse(InvalidFixture(fixture), out _, out var error);
    Assert.That(ok, Is.False);
    Assert.That(error, Is.Not.Empty);
}
```

- [ ] **Step 3: RED 확인**

Run when Unity 6000.3.15f1 is available:

```bash
rtk proxy /Applications/Unity/Hub/Editor/6000.3.15f1/Unity.app/Contents/MacOS/Unity -batchmode -nographics -projectPath /private/tmp/client-crawlstars-sl99/CrawlStars -runTests -testPlatform EditMode -testResults /private/tmp/sl99-editmode.xml -quit
```

Expected: `GameConfig.TryParse`와 `ParsedConfig`가 없어 compile/test FAIL해요. Unity가 설치되지 않은 환경이면 명령 부재를 검증 blocker로 기록하고 test source의 RED 의도는 compile interface로 보존해요.

- [ ] **Step 4: strict parser와 atomic apply 구현**

`GameConfig.cs`의 JSON DTO에 `Required = Required.Always`를 지정하고 `TryParse`에서 다음을 검증해요.

```csharp
public static bool TryParse(string json, out ParsedConfig config, out string error) {
    config = null;
    error = null;
    try {
        ParsedConfig parsed = JsonConvert.DeserializeObject<ParsedConfig>(json);
        Validate(parsed);
        config = parsed;
        return true;
    } catch (Exception exception) when (
        exception is JsonException || exception is InvalidOperationException
    ) {
        error = exception.Message;
        return false;
    }
}
```

`LoadAsync`는 download 성공 뒤 `TryParse`가 성공한 경우에만 static property를 한 번에 apply해 partial state를 남기지 않아요. Exact version `3`, exact unique type set `0/1/2`, finite positive field를 확인해요.

- [ ] **Step 5: Client StreamingAssets를 Server bytes와 동기화**

```bash
rtk cp /Users/hyunjun/Workspace/Server-CrawlStars/.worktrees/sl-99-client-config-contract/client-config/game-config.json /private/tmp/client-crawlstars-sl99/CrawlStars/Assets/StreamingAssets/game-config.json
```

- [ ] **Step 6: parser GREEN 확인**

Unity가 있으면 Step 3 명령을 다시 실행해 PASS를 확인해요. 없으면 C# diff와 fixture byte equality를 검증하고 Unity EditMode 실행 미확인을 명시해요.

- [ ] **Step 7: Client parser commit**

```bash
rtk git -C /private/tmp/client-crawlstars-sl99 add CrawlStars/Assets/Scripts/Core/GameConfig.cs CrawlStars/Assets/Editor/Tests/Core/GameConfigTests.cs CrawlStars/Assets/Editor/Tests/Core/GameConfigTests.cs.meta CrawlStars/Assets/StreamingAssets/game-config.json
rtk git -C /private/tmp/client-crawlstars-sl99 commit -m "[SL-99] feat(config): v3 runtime parser 검증" -m "- 필수 필드와 버전, 캐릭터 타입 검증
- parse 성공 뒤에만 runtime 설정 적용"
```

---

### Task 4: Unity build preprocessor write-before-validate 방지

**Files:**
- Modify: `CrawlStars/Assets/Editor/FileSynchronizer.cs`
- Create: `CrawlStars/Assets/Editor/Tests/Core/FileSynchronizerTests.cs`
- Create: `CrawlStars/Assets/Editor/Tests/Core/FileSynchronizerTests.cs.meta`

**Interfaces:**
- Produces: `internal static void FileSynchronizer.WriteDownloadedFile(string content, string outputPath)`
- Consumes: `GameConfig.TryParse`
- Guarantees: invalid client config는 기존 `StreamingAssets` file을 덮어쓰지 않음

- [ ] **Step 1: invalid download preservation RED test 작성**

```csharp
[Test]
public void WriteDownloadedFile_InvalidGameConfig_PreservesExistingFile() {
    string directory = Path.Combine(Path.GetTempPath(), Guid.NewGuid().ToString("N"));
    Directory.CreateDirectory(directory);
    string path = Path.Combine(directory, "game-config.json");
    try {
        File.WriteAllText(path, "existing");
        Assert.Throws<InvalidOperationException>(() =>
            FileSynchronizer.WriteDownloadedFile("{\"version\":2}", path));
        Assert.That(File.ReadAllText(path), Is.EqualTo("existing"));
    } finally {
        Directory.Delete(directory, true);
    }
}
```

- [ ] **Step 2: RED 확인**

Task 3의 Unity EditMode 명령을 실행해 `WriteDownloadedFile` 부재 failure를 확인해요.

- [ ] **Step 3: validate-before-write 구현**

`SynchronizeFiles`는 download 후 아래 helper만 호출하게 해요.

```csharp
internal static void WriteDownloadedFile(string content, string outputPath) {
    if (Path.GetFileName(outputPath) == "game-config.json" &&
        !GameConfig.TryParse(content, out _, out string error)) {
        throw new InvalidOperationException($"Invalid game config: {error}");
    }
    File.WriteAllText(outputPath, content);
}
```

Game config validation이 write보다 먼저 실행되도록 유지하고, API YAML sync 경로는 변경하지 않아요.

- [ ] **Step 4: GREEN 확인과 byte equality 확인**

```bash
rtk cmp /Users/hyunjun/Workspace/Server-CrawlStars/.worktrees/sl-99-client-config-contract/client-config/game-config.json /private/tmp/client-crawlstars-sl99/CrawlStars/Assets/StreamingAssets/game-config.json
rtk git -C /private/tmp/client-crawlstars-sl99 diff --check
```

Unity가 있으면 EditMode 전체 suite도 PASS해야 해요.

- [ ] **Step 5: Client preprocessor commit**

```bash
rtk git -C /private/tmp/client-crawlstars-sl99 add CrawlStars/Assets/Editor/FileSynchronizer.cs CrawlStars/Assets/Editor/Tests/Core/FileSynchronizerTests.cs CrawlStars/Assets/Editor/Tests/Core/FileSynchronizerTests.cs.meta
rtk git -C /private/tmp/client-crawlstars-sl99 commit -m "[SL-99] fix(build): config 검증 뒤 artifact 반영" -m "- invalid download가 기존 StreamingAssets를 덮지 않게 변경
- build preprocessor 회귀 test 추가"
```

---

### Task 5: Cross-repo 최종 검증과 전달

**Files:**
- Verify only: Server-CrawlStars 전체 diff
- Verify only: Client-CrawlStars 전체 diff
- Update after evidence: Linear SL-99 comment

**Interfaces:**
- Consumes: Server v3 artifact와 Go validator
- Consumes: Client strict runtime/build parser
- Produces: 재현 가능한 validation evidence

- [ ] **Step 1: Server 전체 validation**

```bash
rtk make ci
rtk git diff --check origin/main...HEAD
rtk git diff --exit-code origin/main -- api/openapi.yaml api/asyncapi.yaml
rtk git status --short
```

Expected: `make ci` exit 0, whitespace error 없음, wire spec diff 없음, 의도한 파일만 존재해요.

- [ ] **Step 2: Client 전체 validation**

```bash
rtk cmp client-config/game-config.json /private/tmp/client-crawlstars-sl99/CrawlStars/Assets/StreamingAssets/game-config.json
rtk git -C /private/tmp/client-crawlstars-sl99 diff --check origin/main...HEAD
rtk git -C /private/tmp/client-crawlstars-sl99 status --short
```

Unity 6000.3.15f1이 있으면 EditMode suite를 실행하고 결과 XML의 failure가 0인지 확인해요. 설치되어 있지 않으면 그 사실을 claim과 Linear comment에 명시해요.

- [ ] **Step 3: requirement checklist 재검토**

- Shelly/Colt/Lily exact field와 값이 v3에서 parse됨
- old version/missing/null/invalid numeric/duplicate type가 실패함
- build write 전에 같은 validator가 실행됨
- client-shared 값과 server-authoritative 값이 문서에서 분리됨
- REST/WebSocket 계약과 server simulation은 바뀌지 않음

- [ ] **Step 4: Linear evidence 남기기**

SL-99에 Server/Client branch, validation command와 결과, Unity 실행 여부를 짧게 기록해요. PR merge 전에는 `In Review` 또는 `Done`으로 옮기지 않아요.
