package service

import (
	"archive/zip"
	"bytes"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type webChatUploadValidator uint8

const (
	webChatValidateImage webChatUploadValidator = iota + 1
	webChatValidatePDF
	webChatValidateText
	webChatValidateOLE
	webChatValidateRTF
	webChatValidateZIPEntries
	webChatValidateODT
	webChatValidatePages
	webChatValidateKeynote
)

type webChatUploadType struct {
	CanonicalContentType  string
	AcceptedContentTypes  []string
	Kind                  string
	TextPreviewEnabled    bool
	AcceptsLegacyProvider bool
	Validator             webChatUploadValidator
	RequiredZIPEntries    []string
}

var webChatUploadTypesByExtension = buildWebChatUploadTypesByExtension()
var webChatUploadTypesByFilename = map[string]webChatUploadType{
	"dockerfile": newWebChatTextUploadType("text/x-dockerfile", false),
}

func newWebChatUploadType(canonical string, aliases []string, kind string, preview, legacy bool, validator webChatUploadValidator, zipEntries ...string) webChatUploadType {
	accepted := append([]string{canonical}, aliases...)
	return webChatUploadType{
		CanonicalContentType: canonical, AcceptedContentTypes: accepted, Kind: kind,
		TextPreviewEnabled: preview, AcceptsLegacyProvider: legacy,
		Validator: validator, RequiredZIPEntries: zipEntries,
	}
}

func newWebChatTextUploadType(canonical string, legacy bool, aliases ...string) webChatUploadType {
	return newWebChatUploadType(canonical, aliases, WebChatAttachmentKindFile, true, legacy, webChatValidateText)
}

func buildWebChatUploadTypesByExtension() map[string]webChatUploadType {
	registry := make(map[string]webChatUploadType)
	register := func(extensions []string, fileType webChatUploadType) {
		for _, extension := range extensions {
			registry[extension] = fileType
		}
	}

	register([]string{".png"}, newWebChatUploadType("image/png", nil, WebChatAttachmentKindImage, false, true, webChatValidateImage))
	register([]string{".jpg", ".jpeg"}, newWebChatUploadType("image/jpeg", nil, WebChatAttachmentKindImage, false, true, webChatValidateImage))
	register([]string{".webp"}, newWebChatUploadType("image/webp", nil, WebChatAttachmentKindImage, false, true, webChatValidateImage))
	register([]string{".gif"}, newWebChatUploadType("image/gif", nil, WebChatAttachmentKindImage, false, true, webChatValidateImage))
	register([]string{".pdf"}, newWebChatUploadType("application/pdf", nil, WebChatAttachmentKindFile, false, true, webChatValidatePDF))

	register([]string{".docx"}, newWebChatUploadType(
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document", nil,
		WebChatAttachmentKindFile, false, true, webChatValidateZIPEntries,
		"[Content_Types].xml", "word/document.xml",
	))
	register([]string{".doc", ".dot"}, newWebChatUploadType(
		"application/msword", nil, WebChatAttachmentKindFile, false, false, webChatValidateOLE,
	))
	register([]string{".rtf"}, newWebChatUploadType(
		"application/rtf", []string{"text/rtf"}, WebChatAttachmentKindFile, false, false, webChatValidateRTF,
	))
	register([]string{".odt"}, newWebChatUploadType(
		"application/vnd.oasis.opendocument.text", nil, WebChatAttachmentKindFile, false, false, webChatValidateODT,
	))
	register([]string{".pages"}, newWebChatUploadType(
		"application/vnd.apple.pages", []string{"application/vnd.apple.iwork"},
		WebChatAttachmentKindFile, false, false, webChatValidatePages,
	))

	register([]string{".pptx"}, newWebChatUploadType(
		"application/vnd.openxmlformats-officedocument.presentationml.presentation", nil,
		WebChatAttachmentKindFile, false, false, webChatValidateZIPEntries,
		"[Content_Types].xml", "ppt/presentation.xml",
	))
	register([]string{".pot", ".ppa", ".pps", ".ppt", ".pwz", ".wiz"}, newWebChatUploadType(
		"application/vnd.ms-powerpoint", nil, WebChatAttachmentKindFile, false, false, webChatValidateOLE,
	))
	register([]string{".key"}, newWebChatUploadType(
		"application/vnd.apple.keynote", []string{"application/vnd.apple.iwork"},
		WebChatAttachmentKindFile, false, false, webChatValidateKeynote,
	))

	register([]string{".xlsx"}, newWebChatUploadType(
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", nil,
		WebChatAttachmentKindFile, false, false, webChatValidateZIPEntries,
		"[Content_Types].xml", "xl/workbook.xml",
	))
	register([]string{".xla", ".xlb", ".xlc", ".xlm", ".xls", ".xlt", ".xlw"}, newWebChatUploadType(
		"application/vnd.ms-excel", nil, WebChatAttachmentKindFile, false, false, webChatValidateOLE,
	))
	register([]string{".csv"}, newWebChatTextUploadType("text/csv", true, "application/csv"))
	register([]string{".tsv"}, newWebChatTextUploadType("text/tsv", false))
	register([]string{".iif"}, newWebChatTextUploadType("text/x-iif", false, "application/x-iif"))

	register([]string{".txt", ".text"}, newWebChatTextUploadType("text/plain", true))
	register([]string{".md", ".markdown"}, newWebChatTextUploadType("text/markdown", true, "text/plain"))
	register([]string{".json"}, newWebChatTextUploadType("application/json", true, "text/plain"))

	for extension, contentType := range webChatCodeContentTypeByExtension {
		aliases := append([]string{"text/plain"}, webChatCodeContentTypeAliasesByExtension[extension]...)
		register([]string{extension}, newWebChatTextUploadType(contentType, false, aliases...))
	}
	return registry
}

var webChatCodeContentTypeByExtension = map[string]string{
	".asm": "text/x-asm", ".bat": "text/x-shellscript", ".c": "text/x-c",
	".cc": "text/x-c++", ".conf": "text/plain", ".cpp": "text/x-c++",
	".css": "text/css", ".cxx": "text/x-c++", ".def": "text/plain",
	".dic": "text/plain", ".eml": "message/rfc822", ".h": "text/x-c",
	".hh": "text/x-c++", ".htm": "text/html", ".html": "text/html",
	".ics": "text/calendar", ".ifb": "text/calendar", ".in": "text/plain",
	".js": "text/javascript", ".ksh": "text/x-shellscript", ".list": "text/plain",
	".log": "text/plain", ".mht": "message/rfc822", ".mhtml": "message/rfc822",
	".mime": "message/rfc822", ".mjs": "text/javascript", ".nws": "text/plain",
	".pl": "text/x-perl", ".py": "text/x-python", ".rst": "text/x-rst",
	".s": "text/x-asm", ".sql": "text/x-sql", ".srt": "application/x-subrip",
	".vcf": "text/x-vcard", ".vtt": "text/vtt", ".xml": "text/xml",
	".ts": "text/x-typescript", ".tsx": "text/tsx", ".jsx": "text/jsx",
	".java": "text/x-java", ".go": "text/x-golang", ".rs": "text/x-rust",
	".scala": "application/x-scala", ".ps1": "application/x-powershell",
	".diff": "text/x-diff", ".patch": "text/x-patch", ".php": "text/x-php",
	".rb": "text/x-ruby", ".sh": "text/x-sh", ".bash": "text/x-bash",
	".zsh": "text/x-zsh", ".tex": "text/x-tex", ".cs": "text/x-csharp",
	".kt": "text/x-kotlin", ".kts": "text/x-kotlin", ".swift": "text/x-swift",
	".lua": "text/x-lua", ".r": "text/x-r", ".jl": "text/x-julia",
	".m": "text/x-objectivec", ".mm": "text/x-objectivec++", ".erl": "text/x-erlang",
	".ex": "text/x-elixir", ".exs": "text/x-elixir", ".hs": "text/x-haskell",
	".clj": "text/x-clojure", ".cljs": "text/x-clojure", ".cljc": "text/x-clojure",
	".groovy": "text/x-groovy", ".dart": "text/x-dart", ".awk": "application/x-awk",
	".hbs": "text/x-handlebars", ".mustache": "text/x-mustache", ".ejs": "text/x-ejs",
	".jinja": "text/x-jinja2", ".jinja2": "text/x-jinja2", ".liquid": "text/x-liquid",
	".erb": "text/x-erb", ".twig": "text/x-twig", ".pug": "text/x-pug",
	".jade": "text/x-jade", ".tmpl": "text/x-tmpl", ".cmake": "text/x-cmake",
	".gradle": "text/x-gradle", ".ini": "text/x-ini", ".properties": "text/x-properties",
	".proto": "text/x-protobuf", ".scss": "text/x-scss", ".sass": "text/x-sass",
	".less": "text/x-less", ".hcl": "text/x-hcl", ".tf": "text/x-terraform",
	".toml": "application/toml", ".graphql": "application/graphql",
	".ndjson": "application/x-ndjson", ".json5": "application/json5",
	".yaml": "application/yaml", ".yml": "application/yaml", ".astro": "text/x-astro",
}

var webChatCodeContentTypeAliasesByExtension = map[string][]string{
	".js":      {"application/javascript"},
	".ts":      {"application/typescript"},
	".rs":      {"application/x-rust"},
	".scala":   {"text/x-scala"},
	".patch":   {"application/x-patch"},
	".php":     {"application/x-php", "application/x-httpd-php", "application/x-httpd-php-source"},
	".sh":      {"text/x-shellscript"},
	".bash":    {"application/x-bash", "text/x-shellscript"},
	".sql":     {"application/x-sql"},
	".r":       {"text/x-R"},
	".awk":     {"text/x-awk"},
	".proto":   {"application/x-protobuf"},
	".tf":      {"application/x-terraform"},
	".toml":    {"application/x-toml", "text/x-toml"},
	".graphql": {"application/x-graphql", "text/x-graphql"},
	".json5":   {"application/x-json5"},
	".yaml":    {"application/x-yaml", "text/x-yaml"},
	".yml":     {"application/x-yaml", "text/x-yaml"},
	".srt":     {"text/srt", "text/x-subrip"},
}

func webChatUploadTypeForFilename(filename string) (webChatUploadType, bool) {
	base := strings.ToLower(strings.TrimSpace(filepath.Base(filename)))
	if fileType, ok := webChatUploadTypesByFilename[base]; ok {
		return fileType, true
	}
	fileType, ok := webChatUploadTypesByExtension[strings.ToLower(filepath.Ext(base))]
	return fileType, ok
}

func (fileType webChatUploadType) acceptsContentType(contentType string) bool {
	for _, accepted := range fileType.AcceptedContentTypes {
		if strings.EqualFold(strings.TrimSpace(accepted), contentType) {
			return true
		}
	}
	return false
}

func webChatAttachmentAllowedForProvider(provider string, attachment WebChatAttachment) bool {
	if attachment.Kind != WebChatAttachmentKindImage && attachment.Kind != WebChatAttachmentKindFile {
		return false
	}
	fileType, ok := webChatUploadTypeForFilename(attachment.Filename)
	if !ok || fileType.Kind != attachment.Kind || !fileType.acceptsContentType(strings.ToLower(strings.TrimSpace(attachment.ContentType))) {
		return false
	}
	if attachment.Kind == WebChatAttachmentKindImage {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(provider), "openai") {
		return true
	}
	return fileType.AcceptsLegacyProvider
}

func isGenericWebChatUploadContentType(contentType string) bool {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "", "application/octet-stream", "binary/octet-stream", "application/x-binary":
		return true
	default:
		return false
	}
}

func webChatBodyLooksText(body []byte) bool {
	return utf8.Valid(body) && !bytes.ContainsRune(body, '\x00')
}

func classifyWebChatUploadContentType(filename, rawContentType string, body []byte) (string, string, bool, error) {
	fileType, ok := webChatUploadTypeForFilename(filename)
	if !ok {
		return "", "", false, ErrWebChatUploadRejected
	}
	contentType, _, err := mime.ParseMediaType(strings.TrimSpace(rawContentType))
	if err != nil && strings.TrimSpace(rawContentType) != "" {
		return "", "", false, ErrWebChatUploadRejected
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if contentType != "" && !isGenericWebChatUploadContentType(contentType) && !fileType.acceptsContentType(contentType) {
		return "", "", false, ErrWebChatUploadRejected
	}
	if !webChatUploadBytesMatchType(fileType, body) {
		return "", "", false, ErrWebChatUploadRejected
	}
	return fileType.CanonicalContentType, fileType.Kind, fileType.TextPreviewEnabled, nil
}

var webChatOLEMagic = []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}

func webChatUploadBytesMatchType(fileType webChatUploadType, body []byte) bool {
	switch fileType.Validator {
	case webChatValidateImage:
		detected := strings.ToLower(strings.TrimSpace(strings.SplitN(http.DetectContentType(body), ";", 2)[0]))
		return detected == fileType.CanonicalContentType
	case webChatValidatePDF:
		return bytes.HasPrefix(body, []byte("%PDF-"))
	case webChatValidateText:
		return webChatBodyLooksText(body)
	case webChatValidateOLE:
		return bytes.HasPrefix(body, webChatOLEMagic)
	case webChatValidateRTF:
		return bytes.HasPrefix(bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf}), []byte("{\\rtf"))
	case webChatValidateZIPEntries:
		return webChatZIPContainsEntries(body, fileType.RequiredZIPEntries...)
	case webChatValidateODT:
		return webChatZIPSmallEntryEquals(body, "mimetype", "application/vnd.oasis.opendocument.text")
	case webChatValidatePages:
		return webChatZIPContainsEntries(body, "Index/Document.iwa")
	case webChatValidateKeynote:
		return webChatZIPContainsEntries(body, "Index/Slide.iwa")
	default:
		return false
	}
}

func webChatZIPContainsEntries(body []byte, required ...string) bool {
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return false
	}
	found := make(map[string]bool, len(required))
	for _, file := range archive.File {
		for _, name := range required {
			if file.Name == name {
				found[name] = true
			}
		}
	}
	for _, name := range required {
		if !found[name] {
			return false
		}
	}
	return true
}

func webChatZIPSmallEntryEquals(body []byte, entryName, expected string) bool {
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return false
	}
	for _, file := range archive.File {
		if file.Name != entryName || file.UncompressedSize64 > 256 {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return false
		}
		value, readErr := io.ReadAll(io.LimitReader(reader, 257))
		closeErr := reader.Close()
		return readErr == nil && closeErr == nil && string(value) == expected
	}
	return false
}
