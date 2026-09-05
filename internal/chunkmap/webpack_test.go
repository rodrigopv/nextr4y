package chunkmap

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestFilenameInventoryWithAliases(t *testing.T) {
	body := `f.u=e=>"static/chunks/"+(({16373:"09c3d4f7"})[e]||e)+"."+({21:"f0cc1801",16373:"2c47bcf7"})[e]+".js",f.p="/f/_next/";`
	m := Parse("https://example.com/f/_next/static/chunks/webpack.js", body)
	require.NotNil(t, m)
	require.Len(t, m.Chunks, 2)
	require.Equal(t, "https://example.com/f/_next/static/chunks/21.f0cc1801.js", m.Chunks["21"])
	require.Equal(t, "https://example.com/f/_next/static/chunks/09c3d4f7.2c47bcf7.js", m.Chunks["16373"])
}
func TestFilenameInventorySimpleAndUnknownExpressions(t *testing.T) {
	m := Parse("https://example.com/f/_next/static/chunks/webpack.js", `x.u=n=>"static/chunks/"+n+"-"+({42:"abc123"})[n]+".js"`)
	require.NotNil(t, m)
	require.Equal(t, "https://example.com/f/_next/static/chunks/42-abc123.js", m.Chunks["42"])
	require.Nil(t, Parse("https://example.com/webpack.js", `x.u=n=>"static/chunks/"+evil()+".js"`))
	require.Nil(t, Parse("https://example.com/webpack.js", `x.u=n=>{while(true){}}`))
}
