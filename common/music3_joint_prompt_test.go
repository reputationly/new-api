package common

import (
	"strings"
	"testing"
)

// Music3 的真实约束是 checkpoint 的联合 token 预算(caption + 歌词拼成一条
// prompt,5000 tokens),不是 MusicModelConfig 那种逐字段字符闸。
// 歌词从顶层 prompt 来,不是 metadata.lyrics。
func TestMusic3JointPromptCountsCaptionAndLyricsTogether(t *testing.T) {
	half := strings.Repeat("词", 2600)
	// 各自都没超,合起来超了 —— 逐字段校验会放行,而引擎会整条拒。
	if err := ValidateMusic3JointPrompt(half, half, "minimax-music3"); err == nil {
		t.Fatal("caption 与歌词合计超限时必须拒绝,逐字段看都不超")
	}
	if err := ValidateMusic3JointPrompt(half, "", "minimax-music3"); err != nil {
		t.Fatalf("单独一个 2600 字未超上限,不该拒: %v", err)
	}
}

// 运营配的 maxChars(现网 600)不参与 Music3 的判定。官方 README 推荐的结构化
// caption 是 250-450 英文词,折算远超 600 字符,而引擎实测能吃下 3268 字符 ——
// 用 600 去卡会把官方推荐路径整个挡掉。
func TestMusic3JointPromptIgnoresOperatorMaxChars(t *testing.T) {
	OptionMapRWMutex.Lock()
	if OptionMap == nil {
		OptionMap = map[string]string{}
	}
	prev := OptionMap["MusicModelConfig"]
	OptionMap["MusicModelConfig"] = `{"models":{"minimax-music3":{"maxChars":600}}}`
	OptionMapRWMutex.Unlock()
	defer func() {
		OptionMapRWMutex.Lock()
		OptionMap["MusicModelConfig"] = prev
		OptionMapRWMutex.Unlock()
	}()

	// 前置断言:这份配置对普通音乐模型确实是生效的 600 —— 否则下面"没被挡掉"
	// 可能只是配置压根没读进来,测了个寂寞。
	if max, ok := MusicMaxCharsForModel("t2m", "minimax-music3"); !ok || max != 600 {
		t.Fatalf("配置未生效,后面的断言不成立: max=%d configured=%v", max, ok)
	}

	caption := strings.Repeat("a", 3268) // 实测能出曲的那个长度
	if err := ValidateMusic3JointPrompt(caption, "", "minimax-music3"); err != nil {
		t.Fatalf("引擎能吃下的 caption 不该被运营的 maxChars 挡掉: %v", err)
	}
}

func TestMusic3JointPromptErrorNamesBothFields(t *testing.T) {
	// 只说"超过上限"而不说是哪两段加起来超的,用户会去缩描述,缩完还是被拒。
	err := ValidateMusic3JointPrompt(strings.Repeat("词", 3000), strings.Repeat("词", 3000), "minimax-music3")
	if err == nil {
		t.Fatal("应当超限")
	}
	for _, want := range []string{"描述", "歌词", "minimax-music3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报错缺少 %q: %s", want, err.Error())
		}
	}
}
