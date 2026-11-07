// Command ngg-node is the node-side control daemon (Linux).
//
//	ngg-node -listen :2222 -cert /etc/ngg/node/node.crt -key /etc/ngg/node/node.key \
//	         -ca /etc/ngg/node/ca.crt -kernel /usr/local/bin/ngg-xray \
//	         -config /etc/ngg/xray/config.json -core-unit ngg-core.service
//
// It is installed and started by the deploy script the panel generates; the
// panel then dials it over mutual TLS to push the kernel config and read the
// node's real metrics. See internal/nodesvc for the API contract.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ngg-project/ngg/internal/nodesvc"
	"github.com/ngg-project/ngg/internal/version"
)

func main() {
	var (
		listen    = flag.String("listen", ":2222", "节点 API 监听地址（NODE_PORT）")
		certFile  = flag.String("cert", "/etc/ngg/node/node.crt", "节点证书（PEM）")
		keyFile   = flag.String("key", "/etc/ngg/node/node.key", "节点私钥（PEM，0600）")
		caFile    = flag.String("ca", "/etc/ngg/node/ca.crt", "面板 CA 证书（PEM）")
		manifest  = flag.String("manifest", "/etc/ngg/node.json", "节点清单，用于核对身份")
		kernelBin = flag.String("kernel", "/usr/local/bin/ngg-xray", "内核可执行文件")
		coreConf  = flag.String("config", "/etc/ngg/xray/config.json", "内核配置文件")
		coreUnit  = flag.String("core-unit", "ngg-core.service", "内核 systemd 单元")
		showVer   = flag.Bool("version", false, "打印版本后退出")
	)
	flag.Parse()

	if *showVer {
		fmt.Printf("ngg-node %s\n", version.Release)
		return
	}

	svc, err := nodesvc.New(nodesvc.Config{
		Listen:       *listen,
		CertFile:     *certFile,
		KeyFile:      *keyFile,
		CAFile:       *caFile,
		ManifestPath: *manifest,
		KernelBin:    *kernelBin,
		CoreConfig:   *coreConf,
		CoreUnit:     *coreUnit,
		Version:      version.Release,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ngg-node: %v\n", err)
		os.Exit(1)
	}
	if err := svc.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "ngg-node: %v\n", err)
		os.Exit(1)
	}
}
