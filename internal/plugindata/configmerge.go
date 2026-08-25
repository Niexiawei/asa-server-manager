package plugindata

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// jsonMember 是保序 JSON 对象里的一个成员。
type jsonMember struct {
	Key string
	Raw json.RawMessage
}

// orderedObject 是**保序**的 JSON 对象表示。
//
// 为什么不用 map[string]any：Go 的 map 无序，encoding/json 序列化时按键名排序，
// 合并一次就会把插件配置文件的原有顺序彻底打乱。用户手改过的配置被重排后
// 与文档、与插件作者给的样例都对不上，diff 也没法看。
type orderedObject []jsonMember

var errNotJSONObject = fmt.Errorf("不是 JSON 对象")

// parseOrderedObject 用 json.Decoder 的 token 流把一个 JSON 对象解析成保序表示。
// 值一律保留成 RawMessage：不认识的类型不会在往返中被改写。
func parseOrderedObject(data []byte) (orderedObject, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errNotJSONObject
	}

	obj := orderedObject{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := kt.(string)
		if !ok {
			return nil, fmt.Errorf("对象的键不是字符串: %v", kt)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		obj = append(obj, jsonMember{Key: key, Raw: raw})
	}
	if _, err := dec.Token(); err != nil { // 消费收尾的 '}'
		return nil, err
	}
	// 对象之后不应再有内容
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("JSON 对象之后有多余内容")
	}
	return obj, nil
}

// encodeOrdered 按成员顺序序列化，随后统一缩进。
func encodeOrdered(obj orderedObject) ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range obj {
		if i > 0 {
			b.WriteByte(',')
		}
		k, err := json.Marshal(m.Key)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		var compact bytes.Buffer
		if err := json.Compact(&compact, m.Raw); err != nil {
			return nil, err
		}
		b.Write(compact.Bytes())
	}
	b.WriteByte('}')

	var out bytes.Buffer
	if err := json.Indent(&out, b.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// mergeOrdered 把 incoming（镜像侧，插件运行期回写过的那一份）并进 base（实例侧）。
//
// 取值规则（docs/ARKAPI_PLUGIN_DATA_PLAN.md §4.6，已定策，无例外名单）：
//
//	两侧都有 → 恒取 base（实例侧）的值
//	仅 incoming 有 → 并入，追加在末尾
//	仅 base 有   → 保留（插件删掉的旧项，留着无害）
//
// 这是明确的取舍：用户在管理器里配的东西是权威，插件运行期算出来的值不保留。
//
// 键冲突时仍要**递归**进对象：插件新增的默认项可能落在某个嵌套对象里
// （CrosschatAscended 的 config.json 有近 8 KB，多半是嵌套结构），
// 只做顶层合并会把它们漏掉。数组整体当作一个值，不做逐元素合并 ——
// 逐元素合并需要知道元素的身份键，而那是每个插件各自的约定。
func mergeOrdered(base, incoming orderedObject) orderedObject {
	idx := make(map[string]int, len(base))
	out := make(orderedObject, len(base))
	copy(out, base)
	for i, m := range out {
		idx[m.Key] = i
	}

	for _, m := range incoming {
		i, ok := idx[m.Key]
		if !ok {
			out = append(out, m)
			idx[m.Key] = len(out) - 1
			continue
		}

		baseChild, err1 := parseOrderedObject(out[i].Raw)
		inChild, err2 := parseOrderedObject(m.Raw)
		if err1 != nil || err2 != nil {
			continue // 至少一侧不是对象 → 实例侧整体胜出
		}
		if merged, err := encodeOrdered(mergeOrdered(baseChild, inChild)); err == nil {
			out[i].Raw = merged
		}
	}
	return out
}

// MergeConfigJSON 合并两份配置文件的内容，instanceSide 优先。
// 任意一侧不是 JSON 对象时返回错误，调用方应当保留实例侧原文而不是猜。
func MergeConfigJSON(instanceSide, mirrorSide []byte) ([]byte, error) {
	base, err := parseOrderedObject(instanceSide)
	if err != nil {
		return nil, fmt.Errorf("解析实例侧配置失败: %w", err)
	}
	incoming, err := parseOrderedObject(mirrorSide)
	if err != nil {
		return nil, fmt.Errorf("解析镜像侧配置失败: %w", err)
	}
	return encodeOrdered(mergeOrdered(base, incoming))
}
