package service

import "context"

// ArchiveWriter 把一条 CaptureRecord 写入归档存储。实现必须自身吸收所有
// 错误（不得外溢到调用方转发路径）；此处返回的 error 仅用于内部计数/日志。
type ArchiveWriter interface {
	Write(ctx context.Context, rec *CaptureRecord) error
	Stop()
}

// noopArchiveWriter 在 capture 关闭时注入，零副作用。
type noopArchiveWriter struct{}

func (noopArchiveWriter) Write(context.Context, *CaptureRecord) error { return nil }
func (noopArchiveWriter) Stop()                                       {}
