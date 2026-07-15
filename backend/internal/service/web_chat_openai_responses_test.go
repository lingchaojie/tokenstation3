package service

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadWebChatStoredAttachmentAcceptsPDFAndDOCX(t *testing.T) {
	docx := testWebChatDOCX(t)
	pdf := []byte("%PDF-1.7\n%%EOF")
	storage := &fakeWebChatStorage{
		t:            t,
		files:        map[string][]byte{"paper.pdf": pdf, "notes.docx": docx},
		metaSizes:    map[string]int64{"paper.pdf": int64(len(pdf)), "notes.docx": int64(len(docx))},
		expectedKeys: []string{"paper.pdf", "notes.docx"},
	}

	gotPDF, pdfType, err := readWebChatStoredAttachment(context.Background(), storage, WebChatAttachment{
		Kind: WebChatAttachmentKindFile, ContentType: "application/pdf", StorageKey: "paper.pdf",
	})
	require.NoError(t, err)
	require.Equal(t, pdf, gotPDF)
	require.Equal(t, "application/pdf", pdfType)

	gotDOCX, docxType, err := readWebChatStoredAttachment(context.Background(), storage, WebChatAttachment{
		Kind:        WebChatAttachmentKindFile,
		ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		StorageKey:  "notes.docx",
	})
	require.NoError(t, err)
	require.Equal(t, docx, gotDOCX)
	require.Equal(t, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", docxType)
}

func TestReadWebChatStoredAttachmentRejectsDeclaredPDFWithNonPDFBytes(t *testing.T) {
	storage := fakeWebChatStorageWithFile(t, "paper.pdf", []byte("not a pdf"))

	data, contentType, err := readWebChatStoredAttachment(context.Background(), storage, WebChatAttachment{
		Kind: WebChatAttachmentKindFile, ContentType: "application/pdf", StorageKey: "paper.pdf",
	})

	require.ErrorIs(t, err, ErrWebChatUploadRejected)
	require.Nil(t, data)
	require.Empty(t, contentType)
}

func testWebChatDOCX(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range map[string]string{
		"[Content_Types].xml": "<Types/>",
		"word/document.xml":   "<w:document/>",
	} {
		entry, err := writer.Create(name)
		require.NoError(t, err)
		_, err = entry.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}
