package agent

import (
	"net/http"
	_ "net/http/pprof"

	"go.uber.org/zap"
)

// startPprof 启动 pprof 调试端点
func (a *Agent) startPprof(listen string) {
	a.logger.Info("pprof debug server starting", zap.String("listen", listen))

	server := &http.Server{Addr: listen, Handler: http.DefaultServeMux}

	go func() {
		<-a.ctx.Done()
		server.Close()
	}()

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.logger.Error("pprof server error", zap.Error(err))
		}
		a.logger.Debug("pprof server stopped")
	}()
}
