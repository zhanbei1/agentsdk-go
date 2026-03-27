package api

import "testing"

func TestIsSkylarkSimplePrompt(t *testing.T) {
	o := &SkylarkOptions{SimplePromptMaxRunes: 20}
	if !isSkylarkSimplePrompt("查看网络", o) {
		t.Fatal("expected short prompt")
	}
	if isSkylarkSimplePrompt("这是一段超过二十个汉字长度的用户请求内容测试", o) {
		t.Fatal("expected long prompt rejected")
	}
	o2 := &SkylarkOptions{SimplePromptMaxRunes: 80, ComplexityHints: []string{"分析"}}
	if isSkylarkSimplePrompt("请分析架构", o2) {
		t.Fatal("expected complexity hint to force progressive")
	}
	if isSkylarkSimplePrompt("hello", &SkylarkOptions{EnableOneShotRouting: boolPtr(false)}) {
		t.Fatal("expected disabled routing")
	}
	if !isSkylarkSimplePrompt("hi", &SkylarkOptions{}) {
		t.Fatal("expected nil EnableOneShotRouting to default on")
	}
	// default max 10 runes: 11+ runes should fail
	if isSkylarkSimplePrompt("一二三四五六七八九十终", &SkylarkOptions{}) {
		t.Fatal("expected >10 runes to skip one-shot")
	}
}
