package service

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWebChatUploadRegistryContainsOfficialLocalFormats(t *testing.T) {
	expected := []string{
		".pdf", ".doc", ".docx", ".dot", ".odt", ".rtf", ".pages",
		".pot", ".ppa", ".pps", ".ppt", ".pptx", ".pwz", ".wiz", ".key",
		".xla", ".xlb", ".xlc", ".xlm", ".xls", ".xlsx", ".xlt", ".xlw",
		".csv", ".tsv", ".iif",
		".asm", ".bat", ".c", ".cc", ".conf", ".cpp", ".css", ".cxx",
		".def", ".dic", ".eml", ".h", ".hh", ".htm", ".html", ".ics",
		".ifb", ".in", ".js", ".json", ".ksh", ".list", ".log", ".markdown",
		".md", ".mht", ".mhtml", ".mime", ".mjs", ".nws", ".pl", ".py",
		".rst", ".s", ".sql", ".srt", ".text", ".txt", ".vcf", ".vtt", ".xml",
		".ts", ".tsx", ".jsx", ".java", ".go", ".rs", ".scala", ".ps1",
		".diff", ".patch", ".php", ".rb", ".sh", ".bash", ".zsh", ".tex",
		".cs", ".kt", ".kts", ".swift", ".lua", ".r", ".jl", ".m", ".mm",
		".erl", ".ex", ".exs", ".hs", ".clj", ".cljs", ".cljc", ".groovy",
		".dart", ".awk", ".hbs", ".mustache", ".ejs", ".jinja", ".jinja2",
		".liquid", ".erb", ".twig", ".pug", ".jade", ".tmpl", ".cmake",
		".gradle", ".ini", ".properties", ".proto", ".scss", ".sass", ".less",
		".hcl", ".tf", ".toml", ".graphql", ".ndjson", ".json5", ".yaml",
		".yml", ".astro",
	}
	for _, extension := range expected {
		_, ok := webChatUploadTypeForFilename("attachment" + extension)
		require.Truef(t, ok, "missing registry extension %s", extension)
	}
	_, ok := webChatUploadTypeForFilename("Dockerfile")
	require.True(t, ok)
}

func TestWebChatUploadRegistryExposesCanonicalAliasesAndLegacyMetadata(t *testing.T) {
	cases := []struct {
		filename        string
		canonical       string
		accepted        string
		kind            string
		preview, legacy bool
	}{
		{"photo.png", "image/png", "image/png", WebChatAttachmentKindImage, false, true},
		{"notes.md", "text/markdown", "text/plain", WebChatAttachmentKindFile, true, true},
		{"slides.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation", "application/vnd.openxmlformats-officedocument.presentationml.presentation", WebChatAttachmentKindFile, false, false},
		{"script.js", "text/javascript", "application/javascript", WebChatAttachmentKindFile, true, false},
		{"Dockerfile", "text/x-dockerfile", "text/x-dockerfile", WebChatAttachmentKindFile, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.filename, func(t *testing.T) {
			fileType, ok := webChatUploadTypeForFilename(tc.filename)
			require.True(t, ok)
			require.Equal(t, tc.canonical, fileType.CanonicalContentType)
			require.True(t, fileType.acceptsContentType(tc.accepted))
			require.Equal(t, tc.kind, fileType.Kind)
			require.Equal(t, tc.preview, fileType.TextPreviewEnabled)
			require.Equal(t, tc.legacy, fileType.AcceptsLegacyProvider)
		})
	}
}

func TestClassifyWebChatUploadContentType_AcceptsSupportedContainersAndCode(t *testing.T) {
	ole := append(append([]byte(nil), webChatOLEMagic...), []byte("payload")...)
	cases := []struct {
		name, filename, declared, wantType string
		body                               []byte
	}{
		{"png", "photo.png", "IMAGE/PNG; charset=binary", "image/png", []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}},
		{"pdf", "paper.pdf", "application/pdf", "application/pdf", []byte("%PDF-1.7\n%%EOF")},
		{"docx", "notes.docx", "application/octet-stream", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", testWebChatZIP(t, map[string]string{"[Content_Types].xml": "<Types/>", "word/document.xml": "<w:document/>"})},
		{"pptx", "slides.pptx", "application/octet-stream", "application/vnd.openxmlformats-officedocument.presentationml.presentation", testWebChatZIP(t, map[string]string{"[Content_Types].xml": "<Types/>", "ppt/presentation.xml": "<p:presentation/>"})},
		{"xlsx", "sheet.xlsx", "", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", testWebChatZIP(t, map[string]string{"[Content_Types].xml": "<Types/>", "xl/workbook.xml": "<workbook/>"})},
		{"legacy doc", "notes.doc", "application/msword", "application/msword", ole},
		{"legacy ppt", "slides.ppt", "application/vnd.ms-powerpoint", "application/vnd.ms-powerpoint", ole},
		{"legacy xls", "sheet.xls", "application/vnd.ms-excel", "application/vnd.ms-excel", ole},
		{"odt", "notes.odt", "application/vnd.oasis.opendocument.text", "application/vnd.oasis.opendocument.text", testWebChatZIP(t, map[string]string{"mimetype": "application/vnd.oasis.opendocument.text"})},
		{"rtf alias", "notes.rtf", "text/rtf", "application/rtf", []byte("{\\rtf1 hello}")},
		{"pages alias", "notes.pages", "application/vnd.apple.iwork", "application/vnd.apple.pages", testWebChatZIP(t, map[string]string{"Index/Document.iwa": "data"})},
		{"keynote", "slides.key", "application/vnd.apple.keynote", "application/vnd.apple.keynote", testWebChatZIP(t, map[string]string{"Index/Slide.iwa": "data"})},
		{"python generic", "script.py", "text/plain", "text/x-python", []byte("print('ok')\n")},
		{"javascript alias", "script.js", "application/javascript", "text/javascript", []byte("console.log('ok')\n")},
		{"dockerfile", "Dockerfile", "application/octet-stream", "text/x-dockerfile", []byte("FROM scratch\n")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, kind, _, err := classifyWebChatUploadContentType(tc.filename, tc.declared, tc.body)
			require.NoError(t, err)
			require.Equal(t, tc.wantType, got)
			if tc.name == "png" {
				require.Equal(t, WebChatAttachmentKindImage, kind)
			} else {
				require.Equal(t, WebChatAttachmentKindFile, kind)
			}
		})
	}
}

func TestClassifyWebChatUploadContentType_RejectsUnknownCorruptAndMismatchedFiles(t *testing.T) {
	cases := []struct {
		name, filename, declared string
		body                     []byte
	}{
		{"missing filename", "", "application/pdf", []byte("%PDF-1.7\n%%EOF")},
		{"archive", "archive.zip", "application/zip", []byte("PK\x03\x04")},
		{"executable", "program.exe", "application/octet-stream", []byte("MZ")},
		{"audio", "sample.mp3", "audio/mpeg", []byte("ID3")},
		{"broken image", "photo.png", "image/png", []byte("not a png")},
		{"broken docx", "notes.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", testWebChatZIP(t, map[string]string{"[Content_Types].xml": "<Types/>"})},
		{"broken pptx", "slides.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation", testWebChatZIP(t, map[string]string{"[Content_Types].xml": "<Types/>"})},
		{"broken xlsx", "sheet.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", testWebChatZIP(t, map[string]string{"[Content_Types].xml": "<Types/>"})},
		{"broken odt", "notes.odt", "application/vnd.oasis.opendocument.text", testWebChatZIP(t, map[string]string{"mimetype": "wrong"})},
		{"oversized odt mimetype", "notes.odt", "application/vnd.oasis.opendocument.text", testWebChatZIP(t, map[string]string{"mimetype": string(bytes.Repeat([]byte{'x'}, 257))})},
		{"broken pages", "notes.pages", "application/vnd.apple.pages", testWebChatZIP(t, map[string]string{"Index/Other.iwa": "data"})},
		{"broken keynote", "slides.key", "application/vnd.apple.keynote", testWebChatZIP(t, map[string]string{"Index/Other.iwa": "data"})},
		{"fake pdf", "paper.pdf", "application/pdf", []byte("not a pdf")},
		{"fake ole", "notes.doc", "application/msword", []byte("not ole")},
		{"fake rtf", "notes.rtf", "application/rtf", []byte("not rtf")},
		{"nul code", "script.py", "text/plain", []byte{'p', 0, 'y'}},
		{"invalid utf8 code", "script.py", "text/plain", []byte{0xff}},
		{"mime mismatch", "script.py", "application/pdf", []byte("print('ok')")},
		{"extension mismatch", "paper.txt", "application/pdf", []byte("%PDF-1.7\n%%EOF")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			contentType, kind, preview, err := classifyWebChatUploadContentType(tc.filename, tc.declared, tc.body)
			require.ErrorIs(t, err, ErrWebChatUploadRejected)
			require.Empty(t, contentType)
			require.Empty(t, kind)
			require.False(t, preview)
		})
	}
}

func testWebChatZIP(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for name, body := range entries {
		entry, err := archive.Create(name)
		require.NoError(t, err)
		_, err = entry.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, archive.Close())
	return buffer.Bytes()
}
