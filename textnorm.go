package main

// textnorm.go 精准模式（Qwen3-ASR）输出的数字是汉字，这里把“像数量”的改成阿拉伯数字，和快速模式（SenseVoice 自带转换）风格一致。
// 宁可少改不乱改：只在后面跟着单位（号、月、点、岁、万、元、%…）时才改，成语、“一下子”“千万别”“十分”“三个人”这类不动。

import (
	"regexp"
	"strconv"
	"strings"
)

const cnDigits = "零〇一二两三四五六七八九"

var (
	cnNum = regexp.MustCompile(`[零〇一二两三四五六七八九十百千万亿]+`)
	// 数字后面跟这些才当数量改（长的写前面）
	cnUnits     = []string{"个月", "个小时", "小时", "分钟", "公里", "公斤", "周年", "多万", "多亿", "号", "月", "日", "年", "岁", "元", "块", "倍", "天", "秒", "米", "斤", "期", "集", "点", "%", "％"}
	reCNPercent = regexp.MustCompile(`百分之([零〇一二两三四五六七八九十百千点]+)`)
)

func cnDigit(r rune) int {
	switch r {
	case '零', '〇':
		return 0
	case '一':
		return 1
	case '二', '两':
		return 2
	case '三':
		return 3
	case '四':
		return 4
	case '五':
		return 5
	case '六':
		return 6
	case '七':
		return 7
	case '八':
		return 8
	case '九':
		return 9
	}
	return -1
}

// parseCN 汉字数字 → 整数。“二零二六”这种逐位读的也支持。ok=false 表示不像数字。
func parseCN(s string) (int64, bool) {
	rs := []rune(s)
	if len(rs) == 0 {
		return 0, false
	}
	plain := true // 只有 0~9，没有十百千万
	for _, r := range rs {
		if cnDigit(r) < 0 {
			plain = false
		}
	}
	if plain {
		if len(rs) == 1 {
			return int64(cnDigit(rs[0])), true
		}
		var v int64
		for _, r := range rs {
			v = v*10 + int64(cnDigit(r))
		}
		return v, true
	}
	var total, section, num int64
	for i, r := range rs {
		if d := cnDigit(r); d >= 0 {
			num = int64(d)
			continue
		}
		switch r {
		case '十', '百', '千':
			unit := map[rune]int64{'十': 10, '百': 100, '千': 1000}[r]
			if i == 0 && r != '十' {
				return 0, false // “千万”“百万”开头的多半是词，不是数
			}
			if num == 0 && r == '十' {
				num = 1 // 十二 = 12、二百一十 = 210
			}
			section += num * unit
			num = 0
		case '万', '亿':
			unit := map[rune]int64{'万': 10000, '亿': 100000000}[r]
			section += num
			if section == 0 {
				return 0, false
			}
			if r == '亿' {
				total = (total + section) * unit
			} else {
				total += section * unit
			}
			section, num = 0, 0
		}
	}
	return total + section + num, true
}

func startsWithNumber(s string) bool {
	r := []rune(s)[0]
	return cnDigit(r) >= 0 || r == '十'
}

// normalizeNumbers 把句子里“像数量”的汉字数字换成阿拉伯数字。
func normalizeNumbers(text string) string {
	text = reCNPercent.ReplaceAllStringFunc(text, func(m string) string {
		n := strings.TrimPrefix(m, "百分之")
		if a, b, ok := strings.Cut(n, "点"); ok {
			va, oka := parseCN(a)
			vb, okb := parseCN(b)
			if oka && okb && strings.IndexFunc(b, func(r rune) bool { return cnDigit(r) < 0 }) < 0 {
				return strconv.FormatInt(va, 10) + "." + strconv.FormatInt(vb, 10) + "%"
			}
			return m
		}
		if v, ok := parseCN(n); ok {
			return strconv.FormatInt(v, 10) + "%"
		}
		return m
	})
	var out strings.Builder
	last := 0
	for _, loc := range cnNum.FindAllStringIndex(text, -1) {
		run := text[loc[0]:loc[1]]
		rest := text[loc[1]:]
		repl, ok := convertRun(run, rest)
		if !ok {
			continue
		}
		out.WriteString(text[last:loc[0]])
		out.WriteString(repl)
		last = loc[1]
	}
	out.WriteString(text[last:])
	return out.String()
}

// convertRun 一段汉字数字（run）后面是 rest，决定要不要改、改成什么。
func convertRun(run, rest string) (string, bool) {
	if !startsWithNumber(run) {
		return "", false
	}
	rs := []rune(run)
	// 以万/亿结尾：五千七百万 → 5700万；“一万”也算
	if l := rs[len(rs)-1]; l == '万' || l == '亿' {
		inner := string(rs[:len(rs)-1])
		if inner == "" || strings.ContainsAny(inner, "万亿") {
			return "", false
		}
		v, ok := parseCN(inner)
		if !ok {
			return "", false
		}
		return strconv.FormatInt(v, 10) + string(l), true
	}
	unit := ""
	for _, u := range cnUnits {
		if strings.HasPrefix(rest, u) {
			unit = u
			break
		}
	}
	if unit == "" {
		return "", false
	}
	single := len(rs) == 1
	if single && (rs[0] == '一' || rs[0] == '两') { // 一年一度、一天、两年……保持原样
		return "", false
	}
	if unit == "点" && single && rs[0] == '一' {
		return "", false
	}
	v, ok := parseCN(run)
	if !ok {
		return "", false
	}
	return strconv.FormatInt(v, 10), true
}
