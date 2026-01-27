package infrastructure

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

type ChromedpRenderer struct{}

func NewChromedpRenderer() *ChromedpRenderer { return &ChromedpRenderer{} }

func (r *ChromedpRenderer) RenderHTMLToPDF(ctx context.Context, html string) ([]byte, error) {
	// prepare exec allocator with optional CHROME_PATH
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	if p := os.Getenv("CHROME_PATH"); p != "" {
		opts = append(opts, chromedp.ExecPath(p))
	}

	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()

	cctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()

	// ensure Chrome starts
	ctx2, cancel2 := context.WithTimeout(cctx, 60*time.Second)
	defer cancel2()

	// write HTML to a temporary directory and copy style.css if available
	tmpDir, err := os.MkdirTemp("/tmp", "resume-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	htmlPath := filepath.Join(tmpDir, "index.html")
	if err := os.WriteFile(htmlPath, []byte(html), 0o644); err != nil {
		return nil, err
	}

	candidates := []string{"./templates/style.css", "templates/style.css", "/app/templates/style.css", "./style.css", "style.css"}
	for _, c := range candidates {
		if b, err := os.ReadFile(c); err == nil {
			_ = os.WriteFile(filepath.Join(tmpDir, "style.css"), b, 0o644)
			break
		}
	}

	var pdfBuf []byte
	htmlURL := "file://" + htmlPath
	err = chromedp.Run(ctx2,
		chromedp.Navigate(htmlURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			// A4: 210mm x 297mm -> inches: 8.27 x 11.69
			pdfBuf, _, err = page.PrintToPDF().WithPrintBackground(true).
				WithPaperWidth(8.27).
				WithPaperHeight(11.69).
				WithPreferCSSPageSize(true).
				Do(ctx)
			return err
		}),
	)
	if err != nil {
		return nil, err
	}
	return pdfBuf, nil
}
