package kb

import (
	"strings"
	"sync"

	"github.com/go-ego/gse"
)

var (
	segOnce   sync.Once
	segmenter *gse.Segmenter
	segErr    error
)

// getSegmenter 懒初始化 gse 分词器（内嵌词典，零外部文件，首次加载需数秒）。
func getSegmenter() (*gse.Segmenter, error) {
	segOnce.Do(func() {
		// NewEmbed("zh_s") 使用 go:embed 内嵌的简体词典字符串构造，
		// 不触碰文件系统。gse.New() 无参会走文件加载器，部署环境
		// 没有模块缓存路径时会失败（路由器部署实测踩坑）。
		seg, err := gse.NewEmbed("zh_s")
		if err != nil {
			segErr = err
			return
		}
		seg.LoadStopEmbed()
		segmenter = &seg
	})
	return segmenter, segErr
}

// Tokenize 把文本切成空格分隔的词（含复合词与子词），供 FTS5 索引与查询构造。
// "SQLite轻量级数据库" → "sqlite 轻量 量级 轻量级 数据 据库 数据库"
func Tokenize(text string) (string, error) {
	seg, err := getSegmenter()
	if err != nil {
		return "", err
	}
	words := seg.CutSearch(text)
	var kept []string
	for _, w := range words {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		kept = append(kept, w)
	}
	return strings.Join(kept, " "), nil
}

// BuildFTSQuery 把用户查询转成 FTS5 MATCH 表达式。
// 每个词用双引号包裹（防保留字），词间用 OR 连接保证召回率，融合排序交给 RRF。
func BuildFTSQuery(query string) (string, error) {
	tokens, err := Tokenize(query)
	if err != nil {
		return "", err
	}
	if tokens == "" {
		return "", nil
	}
	words := strings.Fields(tokens)
	for i, w := range words {
		words[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
	}
	return strings.Join(words, " OR "), nil
}
