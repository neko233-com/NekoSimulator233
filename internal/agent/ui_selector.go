package agent

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// UISelector fields are exact, case-sensitive matches; nonempty fields are ANDed.
type UISelector struct {
	Text        string `json:"text,omitempty" jsonschema:"Exact visible text"`
	ResourceID  string `json:"resource_id,omitempty" jsonschema:"Exact Android resource-id"`
	Description string `json:"description,omitempty" jsonschema:"Exact Android content-desc"`
	Class       string `json:"class,omitempty" jsonschema:"Optional exact Android class"`
	Package     string `json:"package,omitempty" jsonschema:"Optional exact Android package name"`
}

type UINode struct {
	Text        string `json:"text"`
	ResourceID  string `json:"resource_id"`
	Description string `json:"description"`
	Class       string `json:"class"`
	Package     string `json:"package"`
	Enabled     bool   `json:"enabled"`
	Clickable   bool   `json:"clickable"`
	Left        int    `json:"left"`
	Top         int    `json:"top"`
	Right       int    `json:"right"`
	Bottom      int    `json:"bottom"`
}

type UIElements struct {
	Elements []UINode `json:"elements"`
}

func (s UISelector) validate() error {
	if s.Text == "" && s.ResourceID == "" && s.Description == "" {
		return errors.New("请提供 text、resource_id 或 description 中至少一项")
	}
	for _, v := range []string{s.Text, s.ResourceID, s.Description, s.Class, s.Package} {
		if len(v) > 1024 {
			return errors.New("UI 选择条件不能超过 1024 字节")
		}
	}
	return nil
}

var uiBounds = regexp.MustCompile(`^\[(\d+),(\d+)\]\[(\d+),(\d+)\]$`)

func selectUI(tree string, selector UISelector) ([]UINode, error) {
	if err := selector.validate(); err != nil {
		return nil, err
	}
	if len(tree) > 4<<20 {
		return nil, errors.New("UI 层级超过 4 MiB")
	}
	dec := xml.NewDecoder(strings.NewReader(tree))
	result := make([]UINode, 0)
	depth, roots := 0, 0
	for {
		token, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("UI 层级格式无效：%w", err)
		}
		switch t := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots != 1 || t.Name.Local != "hierarchy" || t.Name.Space != "" {
					return nil, errors.New("UI 层级根节点无效")
				}
			}
			depth++
			if t.Name.Local != "node" || t.Name.Space != "" || depth < 2 {
				continue
			}
			attrs := make(map[string]string, len(t.Attr))
			for _, a := range t.Attr {
				if a.Name.Space != "" {
					continue
				}
				if _, duplicate := attrs[a.Name.Local]; duplicate {
					return nil, errors.New("UI 节点包含重复属性")
				}
				attrs[a.Name.Local] = a.Value
			}
			if selector.Text != "" && selector.Text != attrs["text"] || selector.ResourceID != "" && selector.ResourceID != attrs["resource-id"] || selector.Description != "" && selector.Description != attrs["content-desc"] || selector.Class != "" && selector.Class != attrs["class"] || selector.Package != "" && selector.Package != attrs["package"] {
				continue
			}
			bounds := uiBounds.FindStringSubmatch(attrs["bounds"])
			if len(bounds) != 5 {
				return nil, errors.New("匹配节点的画面范围无效")
			}
			var p [4]int
			for i := range p {
				v, e := strconv.ParseUint(bounds[i+1], 10, 31)
				if e != nil {
					return nil, errors.New("匹配节点的画面范围无效")
				}
				p[i] = int(v)
			}
			if p[2] <= p[0] || p[3] <= p[1] {
				return nil, errors.New("匹配节点的画面范围为空")
			}
			result = append(result, UINode{Text: attrs["text"], ResourceID: attrs["resource-id"], Description: attrs["content-desc"], Class: attrs["class"], Package: attrs["package"], Enabled: attrs["enabled"] == "true", Clickable: attrs["clickable"] == "true", Left: p[0], Top: p[1], Right: p[2], Bottom: p[3]})
		case xml.EndElement:
			depth--
		}
	}
	if roots != 1 || depth != 0 {
		return nil, errors.New("UI 层级不完整")
	}
	return result, nil
}

func uniqueUINode(nodes []UINode) (UINode, error) {
	if len(nodes) != 1 {
		return UINode{}, fmt.Errorf("需要恰好一个匹配节点，找到 %d 个", len(nodes))
	}
	n := nodes[0]
	if !n.Enabled {
		return UINode{}, errors.New("匹配节点不可用，未点击")
	}
	return n, nil
}

func uiTapPoint(nodes []UINode, width, height int) (int, int, error) {
	n, err := uniqueUINode(nodes)
	if err != nil {
		return 0, 0, err
	}
	left, top := max(0, n.Left), max(0, n.Top)
	right, bottom := min(width, n.Right), min(height, n.Bottom)
	if right <= left || bottom <= top {
		return 0, 0, errors.New("匹配节点不在当前画面内，未点击")
	}
	return left + (right-left-1)/2, top + (bottom-top-1)/2, nil
}
