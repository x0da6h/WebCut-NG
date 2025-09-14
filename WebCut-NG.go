package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io/ioutil"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/jchv/go-webview2"
)

var (
	currentScreenshot []byte
	batchScreenshots  = make(map[string][]byte)
	serverAddr        string
	batchMutex        sync.Mutex
	urlList           []string
	urlListMutex      sync.Mutex
)

func main() {
	// 设置DPI感知
	runtime.LockOSThread()

	// 创建并启动本地HTTP服务器
	serverAddr = startServer()

	// 创建WebView窗口（禁用调试模式）
	w := webview2.New(false)
	defer w.Destroy()

	// 设置窗口标题
	w.SetTitle("WebCut-网页快照")

	// 加载本地服务器的HTML页面
	w.Navigate(fmt.Sprintf("http://%s", serverAddr))

	// 运行WebView主循环
	w.Run()
}

// 启动本地HTTP服务器
type PageData struct {
	ServerAddr string
}

func startServer() string {
	// 初始化浏览器池
	initBrowserPool()

	// 创建一个监听器
	listener, err := net.Listen("tcp", "127.0.0.1:1426")
	if err != nil {
		panic(err)
	}

	// 获取分配的地址和端口
	addr := "127.0.0.1:1426"

	// 定义HTML模板
	htmlTemplate := `
<!DOCTYPE html>
<html lang="zh-CN">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>URL截图查看器</title>
	<style>
		body {
			font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'Roboto', sans-serif;
			padding: 20px;
		}
		.container {
			max-width: 1200px;
			margin: 0 auto;
		}
		button {
			padding: 12px 24px;
			background-color: #3498db;
			color: white;
			border: none;
			border-radius: 4px;
			cursor: pointer;
			margin-right: 10px;
		}
		.message {
			display: none;
			padding: 15px;
			margin: 10px 0;
			border-radius: 4px;
		}
		.message.success {
			background-color: #d4edda;
			color: #155724;
		}
		.message.error {
			background-color: #f8d7da;
			color: #721c24;
		}
		.img-container {
			margin-top: 20px;
			border: 1px solid #ddd;
			border-radius: 4px;
			padding: 10px;
		}
		.img-container img {
			max-width: 100%;
			height: auto;
		}
		.url-list {
			margin-top: 20px;
			padding: 10px;
			border: 1px solid #ddd;
			border-radius: 4px;
			max-height: 300px;
			overflow-y: auto;
		}
		.url-item {
			padding: 8px;
			border-bottom: 1px solid #eee;
			cursor: pointer;
		}
		.url-item:hover {
			background-color: #f5f5f5;
		}
		.url-item:last-child {
			border-bottom: none;
		}
		#fileInput {
			display: none;
		}
		input[type="text"] {
			width: 100%;
			padding: 12px;
			margin-bottom: 10px;
			box-sizing: border-box;
			border: 1px solid #ddd;
			border-radius: 4px;
		}
	</style>
	<script>
	document.addEventListener('DOMContentLoaded', function() {
		var captureBtn = document.getElementById('captureBtn');
		var urlInput = document.getElementById('urlInput');
		var message = document.getElementById('message');
		var imgContainer = document.getElementById('imgContainer');
		var screenshotImg = document.getElementById('screenshotImg');
		var loadListBtn = document.getElementById('loadListBtn');
		var fileInput = document.getElementById('fileInput');
		var urlListElement = document.getElementById('urlList');
		var batchCaptureBtn = document.getElementById('batchCaptureBtn');
		var progressContainer = document.querySelector('.progress');
		var progressText = document.getElementById('progressText');
		var progressBar = document.getElementById('progressBar');
		var batchResultsContainer = document.getElementById('batchResults');
		var screenshotsGrid = document.getElementById('screenshotsGrid');

		// 页面加载完成后，自动获取已加载的URL列表
		fetch('/get-urls', {
			method: 'GET',
			headers: {'Content-Type': 'application/json'}
		}).then(function(response) {
			return response.json();
		}).then(function(data) {
			if (data.urls && data.urls.length > 0) {
				updateUrlList(data.urls);
			}
		}).catch(function(error) {
			console.log('获取URL列表失败: ' + error.message);
		});

		function showMessage(text, isError) {
			message.textContent = text;
			message.className = 'message ' + (isError ? 'error' : 'success');
			message.style.display = 'block';
			setTimeout(function() {
				message.style.display = 'none';
			}, 3000);
		}

		function showScreenshot(base64Image) {
			screenshotImg.src = 'data:image/png;base64,' + base64Image;
			imgContainer.style.display = 'block';
		}

		function updateUrlList(urls) {
			urlListElement.innerHTML = '';
			if (urls && urls.length > 0) {
				urls.forEach(function(url, index) {
					var div = document.createElement('div');
					div.className = 'url-item';
					div.textContent = (index + 1) + '. ' + url;
					div.onclick = function() {
						console.log('点击URL项：', url);
						// 不再需要设置输入框值
					};
					urlListElement.appendChild(div);
				});
			} else {
				var div = document.createElement('div');
				div.className = 'url-item';
				div.textContent = '暂无URL列表，请先加载URL列表文件';
				urlListElement.appendChild(div);
			}
		}

		// 显示单个已完成的截图
		function showCompletedScreenshot(url) {
			// 规范化URL格式，与后端完全一致
			let normalizedUrl = url;
			// 先检测是否是163.com，保留原始URL进行检测
			if (url.toLowerCase().includes('163.com')) {
				normalizedUrl = 'https://www.163.com';
			} else {
				// 移除末尾斜杠
				normalizedUrl = url.endsWith('/') && url.length > 1 ? url.slice(0, -1) : url;
				// 确保包含协议前缀
				if (!normalizedUrl.startsWith('http://') && !normalizedUrl.startsWith('https://')) {
					normalizedUrl = 'http://' + normalizedUrl;
				}
			}
			
			console.log('尝试显示已完成的截图:', normalizedUrl);
			
			// 获取指定URL的截图
			fetch('/get-batch-screenshots', {
				method: 'GET',
				headers: {'Content-Type': 'application/json'}
			}).then(function(response) {
				return response.json();
			}).then(function(data) {
				console.log('获取到的截图数据:', data);
				if (data.screenshots) {
					// 确保截图网格已显示
					batchResultsContainer.style.display = 'block';
					
					// 检查是否已经存在该URL的截图
					var existingItems = screenshotsGrid.querySelectorAll('.screenshot-item');
					var alreadyExists = false;
					for (var i = 0; i < existingItems.length; i++) {
						var p = existingItems[i].querySelector('p');
						if (p && p.textContent === normalizedUrl) {
							alreadyExists = true;
							break;
						}
					}

					if (!alreadyExists) {
						// 创建新的截图容器
						var screenshotContainer = document.createElement('div');
						screenshotContainer.className = 'screenshot-item';
						screenshotContainer.style.border = '1px solid #ddd';
						screenshotContainer.style.borderRadius = '4px';
						screenshotContainer.style.padding = '10px';
						screenshotContainer.style.boxShadow = '0 2px 4px rgba(0,0,0,0.1)';
						screenshotContainer.style.backgroundColor = 'white';
						screenshotContainer.style.display = 'flex';
						screenshotContainer.style.flexDirection = 'column';
					 
						// 添加淡入动画
						screenshotContainer.style.opacity = '0';
						screenshotContainer.style.transform = 'translateY(20px)';
						screenshotContainer.style.transition = 'opacity 0.3s ease, transform 0.3s ease';
					 
						var screenshotImg = document.createElement('img');
						if (data.screenshots[normalizedUrl]) {
							screenshotImg.src = 'data:image/png;base64,' + data.screenshots[normalizedUrl];
						} else {
							// 如果没有找到该URL的截图，显示错误信息
							console.warn('未找到URL的截图:', normalizedUrl);
							// 可以选择使用一个默认的占位图
							screenshotImg.src = 'data:image/svg+xml;charset=utf-8,%3Csvg xmlns="http://www.w3.org/2000/svg" width="1200" height="800" viewBox="0 0 1200 800"%3E%3Crect width="1200" height="800" fill="%23f5f5f5"/%3E%3Ctext x="600" y="400" font-family="Arial" font-size="24" text-anchor="middle" fill="%23666"%3E截图失败%3C/text%3E%3Ctext x="600" y="440" font-family="Arial" font-size="16" text-anchor="middle" fill="%23999"%3E' + encodeURIComponent(normalizedUrl) + '%3C/text%3E%3C/svg%3E';
						}
						screenshotImg.style.maxWidth = '100%';
						screenshotImg.style.height = 'auto';
						screenshotImg.style.marginBottom = '10px';
						screenshotImg.style.borderRadius = '4px';
					 
						var urlText = document.createElement('p');
						urlText.textContent = normalizedUrl;
						urlText.style.fontSize = '12px';
						urlText.style.color = '#666';
						urlText.style.wordBreak = 'break-all';
						urlText.style.margin = '0';
						urlText.style.flexGrow = '1';
					 
						screenshotContainer.appendChild(screenshotImg);
						screenshotContainer.appendChild(urlText);
						screenshotsGrid.appendChild(screenshotContainer);
					 
						// 触发动画
						setTimeout(function() {
							screenshotContainer.style.opacity = '1';
							screenshotContainer.style.transform = 'translateY(0)';
						}, 10);
					}
				} else {
					console.error('获取的截图数据为空');
				}
			}).catch(function(error) {
				console.error('获取截图失败:', error);
				// 即使获取失败，也尝试创建一个错误占位图
				batchResultsContainer.style.display = 'block';
				var existingItems = screenshotsGrid.querySelectorAll('.screenshot-item');
				var alreadyExists = false;
				for (var i = 0; i < existingItems.length; i++) {
					var p = existingItems[i].querySelector('p');
					if (p && p.textContent === normalizedUrl) {
						alreadyExists = true;
						break;
					}
				}
				if (!alreadyExists) {
					var screenshotContainer = document.createElement('div');
					screenshotContainer.className = 'screenshot-item';
					screenshotContainer.style.border = '1px solid #ddd';
					screenshotContainer.style.borderRadius = '4px';
					screenshotContainer.style.padding = '10px';
					screenshotContainer.style.boxShadow = '0 2px 4px rgba(0,0,0,0.1)';
					screenshotContainer.style.backgroundColor = 'white';
					var screenshotImg = document.createElement('img');
					screenshotImg.src = 'data:image/svg+xml;charset=utf-8,%3Csvg xmlns="http://www.w3.org/2000/svg" width="1200" height="800" viewBox="0 0 1200 800"%3E%3Crect width="1200" height="800" fill="%23f5f5f5"/%3E%3Ctext x="600" y="400" font-family="Arial" font-size="24" text-anchor="middle" fill="%23666"%3E获取截图失败%3E%3Ctext x="600" y="440" font-family="Arial" font-size="16" text-anchor="middle" fill="%23999"%3E' + encodeURIComponent(normalizedUrl) + '%3C/text%3E%3C/svg%3E';
					screenshotImg.style.maxWidth = '100%';
					var urlText = document.createElement('p');
					urlText.textContent = normalizedUrl;
					urlText.style.fontSize = '12px';
					urlText.style.color = '#666';
					screenshotContainer.appendChild(screenshotImg);
					screenshotContainer.appendChild(urlText);
					screenshotsGrid.appendChild(screenshotContainer);
				}
			});
		}

		// 显示批量截图结果
		function showBatchScreenshots() {
			fetch('/get-batch-screenshots', {
				method: 'GET',
				headers: {'Content-Type': 'application/json'}
			}).then(function(response) {
				return response.json();
			}).then(function(data) {
				if (data.screenshots && Object.keys(data.screenshots).length > 0) {
					// 清空截图网格
					screenshotsGrid.innerHTML = '';
					
					// 添加每个截图到网格
					for (var url in data.screenshots) {
						if (data.screenshots.hasOwnProperty(url)) {
							var screenshotContainer = document.createElement('div');
							screenshotContainer.className = 'screenshot-item';
							screenshotContainer.style.border = '1px solid #ddd';
							screenshotContainer.style.borderRadius = '4px';
							screenshotContainer.style.padding = '10px';
							screenshotContainer.style.boxShadow = '0 2px 4px rgba(0,0,0,0.1)';
							screenshotContainer.style.backgroundColor = 'white';
							screenshotContainer.style.display = 'flex';
							screenshotContainer.style.flexDirection = 'column';
							
							var screenshotImg = document.createElement('img');
							screenshotImg.src = 'data:image/png;base64,' + data.screenshots[url];
							screenshotImg.style.maxWidth = '100%';
							screenshotImg.style.height = 'auto';
							screenshotImg.style.marginBottom = '10px';
							screenshotImg.style.borderRadius = '4px';
							
							var urlText = document.createElement('p');
							urlText.textContent = url;
							urlText.style.fontSize = '12px';
							urlText.style.color = '#666';
							urlText.style.wordBreak = 'break-all';
							urlText.style.margin = '0';
							urlText.style.flexGrow = '1';
							
							screenshotContainer.appendChild(screenshotImg);
							screenshotContainer.appendChild(urlText);
							screenshotsGrid.appendChild(screenshotContainer);
						}
					}
					
					// 显示批量截图结果区域
					batchResultsContainer.style.display = 'block';
				} else {
					showMessage('暂无批量截图结果', true);
					batchResultsContainer.style.display = 'none';
				}
			}).catch(function(error) {
				console.error('获取批量截图结果失败:', error);
				showMessage('获取批量截图结果失败: ' + error.message, true);
			});
		}

		// 在前端添加一个标准化URL的工具函数，供调试使用
		function debugNormalizeURL(url) {
			let normalizedUrl = url;
			// 先检测是否是163.com，保留原始URL进行检测
			if (url.toLowerCase().includes('163.com')) {
				normalizedUrl = 'https://www.163.com';
			} else {
				// 移除末尾斜杠
				normalizedUrl = url.endsWith('/') && url.length > 1 ? url.slice(0, -1) : url;
				// 确保包含协议前缀
				if (!normalizedUrl.startsWith('http://') && !normalizedUrl.startsWith('https://')) {
					normalizedUrl = 'http://' + normalizedUrl;
				}
			}
			return normalizedUrl;
		}

		// 批量截图按钮点击事件
		batchCaptureBtn.addEventListener('click', function() {
			fetch('/get-urls', {
				method: 'GET',
				headers: {'Content-Type': 'application/json'}
			}).then(function(response) {
				return response.json();
			}).then(function(data) {
				if (!data.urls || data.urls.length === 0) {
					showMessage('请先加载URL列表', true);
					return;
				}

				// 显示进度条
				progressContainer.style.display = 'block';
				progressBar.style.width = '0%';
				progressText.textContent = '准备开始批量截图...';

				// 发送批量截图请求
				fetch('/batch-capture', {
					method: 'POST',
					headers: {'Content-Type': 'application/json'},
					body: JSON.stringify({fullPage: false})
				}).then(function(response) {
					// 处理服务器发送的事件流
					const reader = response.body.getReader();
					const decoder = new TextDecoder();

					function readStream() {
						reader.read().then(({done, value}) => {
							if (done) {
								// 批量截图完成后，获取并显示完整结果
								// 确保所有URL都能正确显示，即使在实时更新过程中有通知丢失
								showBatchScreenshots();
								return;
							}

							const chunk = decoder.decode(value, {stream: true});
							// 处理每一行数据
							const lines = chunk.split('\n');
							lines.forEach(function(line) {
								if (line.trim().startsWith('data:')) {
									try {
										const data = JSON.parse(line.trim().substring(5));
										if (data.status) {
											progressText.textContent = data.status;
										}
										if (data.progress !== undefined) {
											progressBar.style.width = data.progress + '%';
										}
										if (data.error) {
											showMessage(data.error, true);
										}
										 
										// 检查是否有已完成的URL并立即显示截图
										if (data.completedUrl) {
											// 这里我们直接使用原始URL，showCompletedScreenshot函数会进行标准化
											showCompletedScreenshot(data.completedUrl);
										}
										// 检查是否所有截图都已完成
										if (data.allCompleted) {
											// 已经从服务器获取了正确的进度文本，不需要硬编码
											progressBar.style.width = '100%';
											showMessage('批量截图完成', false);
											// 再次调用showBatchScreenshots确保所有URL都能显示
											showBatchScreenshots();
											// 不立即隐藏进度条，让用户看到最终完成状态
											setTimeout(function() {
												progressContainer.style.display = 'none';
											}, 1000);
										}
									} catch (e) {
										console.error('解析进度数据失败:', e);
									}
								}
							});

							readStream();
						});
					}

					readStream();
				}).catch(function(error) {
					showMessage('批量截图失败: ' + error.message, true);
					progressContainer.style.display = 'none';
				});
			});
		});

		loadListBtn.addEventListener('click', function() {
			fileInput.click();
		});

		fileInput.addEventListener('change', function(e) {
			var file = e.target.files[0];
			if (!file) return;

			var reader = new FileReader();
			reader.onload = function(e) {
				var content = e.target.result;
				var urls = content.split('\n').filter(function(line) {
					return line.trim() !== '';
				});

				// 发送URL列表到服务器
				fetch('/load-urls', {
					method: 'POST',
					headers: {'Content-Type': 'application/json'},
					body: JSON.stringify({urls: urls})
				}).then(function(response) {
					return response.json();
				}).then(function(data) {
					if (data.success) {
						showMessage('URL列表加载成功，共 ' + urls.length + ' 个URL');
						updateUrlList(urls);
						// 清空之前的截图结果
						screenshotsGrid.innerHTML = '';
						batchResultsContainer.style.display = 'none';
					} else {
						showMessage(data.error || 'URL列表加载失败', true);
					}
				}).catch(function(error) {
					showMessage('URL列表加载失败: ' + error.message, true);
				});
			};
			reader.readAsText(file);
			this.value = ''; // 重置文件选择器
		});
	});
	</script>
</head>
<body>
	<div class="container">
		<h1>WebCut - 网页快照工具</h1>
		<div>
			<!-- 隐藏URL输入框，因为不再支持单独截图 -->
			<input type="text" id="urlInput" placeholder="请输入要截图的网址" style="display: none;">
		</div>
		<!-- 隐藏单个截图按钮 -->
		<button id="captureBtn" style="display: none;">截取屏幕</button>
		<button id="loadListBtn">加载URL列表</button>
		<button id="batchCaptureBtn">批量截图</button>
		<input type="file" id="fileInput" accept=".txt">
		<div class="message" id="message"></div>
		<div class="progress" style="margin-top: 10px; display: none;">
			<p id="progressText">准备开始批量截图...</p>
			<div style="width: 100%; height: 20px; background-color: #f0f0f0; border-radius: 10px; overflow: hidden;">
				<div id="progressBar" style="height: 100%; width: 0%; background-color: #3498db;"></div>
			</div>
		</div>
		
		<div class="img-container" id="imgContainer" style="display: none;">
			<h3>截图结果</h3>
			<img id="screenshotImg" alt="网页截图">
		</div>
		
		<div class="url-list-container">
			<h3>URL列表</h3>
			<div id="urlList" class="url-list"></div>
		</div>
		
		<!-- 批量截图结果显示区域 -->
		<div id="batchResults" style="margin-top: 20px; display: none;">
			<h3>批量截图结果</h3>
			<div id="screenshotsGrid" style="display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 15px;">
				<!-- 截图结果会动态添加到这里 -->
			</div>
		</div>
	</div>
</body>
</html>
`

	// 加载URL列表
	http.HandleFunc("/load-urls", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			fmt.Printf("读取URL列表请求体失败: %v\n", err)
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}

		var req struct {
			URLs []string `json:"urls"`
		}

		if err := json.Unmarshal(body, &req); err != nil {
			fmt.Printf("URL列表JSON解析错误: %v\n", err)
			http.Error(w, "Invalid JSON format", http.StatusBadRequest)
			return
		}

		urlListMutex.Lock()
		urlList = req.URLs
		urlListMutex.Unlock()

		fmt.Printf("成功加载URL列表，共 %d 个URL\n", len(req.URLs))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	})

	// 获取URL列表
	http.HandleFunc("/get-urls", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		urlListMutex.Lock()
		defer urlListMutex.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"urls": urlList})
	})

	// 处理根路径请求
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		tmpl := template.Must(template.New("page").Parse(htmlTemplate))
		tmpl.Execute(w, PageData{ServerAddr: addr})
	})

	// 处理截图请求（虽然隐藏了按钮，但保留API以便未来可能需要）
	http.HandleFunc("/capture", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// 读取请求体
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}

		// 解析JSON请求
		var req struct {
			URL      string `json:"url"`
			FullPage bool   `json:"fullPage"`
		}

		if err := json.Unmarshal(body, &req); err != nil {
			fmt.Printf("JSON解析错误: %v\n", err)
			http.Error(w, "Invalid JSON format", http.StatusBadRequest)
			return
		}

		fmt.Printf("准备截图URL: %s\n", req.URL)

		// 捕获截图
		imgData, err := captureScreenshot(req.URL, req.FullPage)
		if err != nil {
			fmt.Printf("截图失败: %v\n", err)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("截图失败: %v", err)})
			return
		}

		// 保存当前截图
		currentScreenshot = imgData

		// 将截图转换为base64并返回
		base64Image := base64.StdEncoding.EncodeToString(imgData)
		fmt.Println("截图成功，已返回响应")
		json.NewEncoder(w).Encode(map[string]string{"base64Image": base64Image})
	})

	// 批量截图API
	var batchRunning = false
	var batchMutex sync.Mutex

	// 处理批量截图请求
	http.HandleFunc("/batch-capture", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// 检查是否有批量任务正在运行
		batchMutex.Lock()
		if batchRunning {
			batchMutex.Unlock()
			http.Error(w, "批量截图任务正在运行，请稍后再试", http.StatusConflict)
			return
		}
		batchRunning = true
		batchMutex.Unlock()
		defer func() {
			batchMutex.Lock()
			batchRunning = false
			batchMutex.Unlock()
		}()

		// 先读取请求体，避免在设置SSE响应头后读取导致连接问题
		// 读取请求体
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			fmt.Printf("读取批量截图请求体失败: %v\n", err)
			// 由于请求体读取失败，无法使用SSE，直接返回错误
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}

		// 解析JSON请求
		var req struct {
			FullPage bool `json:"fullPage"`
		}

		if err := json.Unmarshal(body, &req); err != nil {
			fmt.Printf("批量截图JSON解析错误: %v\n", err)
			// 由于JSON解析失败，无法使用SSE，直接返回错误
			http.Error(w, "Invalid JSON format", http.StatusBadRequest)
			return
		}

		// 现在已经成功读取并解析了请求体，才设置SSE响应头
		// 设置响应头为SSE格式
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		// 确保客户端收到响应头
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}

		// 获取URL列表
		urlListMutex.Lock()
		urls := make([]string, len(urlList))
		copy(urls, urlList)
		urlListMutex.Unlock()

		if len(urls) == 0 {
			fmt.Fprintf(w, "data: {\"error\": \"URL列表为空，请先加载URL列表\"}\n\n")
			return
		}

		fmt.Printf("开始批量截图，共 %d 个URL\n", len(urls))

		// 在开始新的批量截图任务前，重置浏览器池，解决URL列表切换后截图失败的问题
		// 这会创建全新的浏览器实例，避免使用可能已损坏的上下文
		resetBrowserPool()

		// 清空之前的批量截图结果
		batchMutex.Lock()
		batchScreenshots = make(map[string][]byte)
		batchMutex.Unlock()

		// 创建完成的URL通道，用于实时获取已完成的截图
		completedURLs := make(chan string, len(urls))
		var wg sync.WaitGroup
		var totalCount = len(urls)

		// 适当降低并发数量，避免资源耗尽导致超时
		// 浏览器池大小为10，但实际运行时应保留一些缓冲
		concurrencyLimit := 5
		semaphore := make(chan struct{}, concurrencyLimit)

		// 启动并发截图
		for _, url := range urls {
			wg.Add(1)
			semaphore <- struct{}{} // 获取令牌
			go func(url string) {
				defer wg.Done()
				defer func() {
					<-semaphore // 释放令牌
					// 确保即使发生panic也能处理
					if r := recover(); r != nil {
						fmt.Printf("处理URL %s 时发生panic: %v\n", url, r)
					}
				}() // 释放令牌

				fmt.Printf("正在截图URL: %s\n", url)

				// 捕获截图
				imgData, err := captureScreenshot(url, req.FullPage)
				if err != nil {
					fmt.Printf("URL %s 截图失败: %v\n", url, err)
					// 使用占位图代替失败的截图
					placeholder := createErrorPlaceholder(1200, 800, url)
					batchMutex.Lock()
					batchScreenshots[normalizeURL(url)] = placeholder
					batchMutex.Unlock()
					fmt.Printf("URL %s 使用占位图\n", url)
				} else {
					// 标准化URL格式并保存截图结果
					batchMutex.Lock()
					batchScreenshots[normalizeURL(url)] = imgData
					batchMutex.Unlock()

					fmt.Printf("URL %s 截图成功\n", url)
				}

				// 发送原始URL到通道，用于准确计数
				completedURLs <- url
			}(url)
		}

		// 启动一个goroutine监听完成的URL并更新进度
		go func() {
			var localProcessedCount = 0
			processedURLs := make(map[string]bool) // 用于跟踪已处理的原始URL
			for originalUrl := range completedURLs {
				// 对原始URL进行计数，确保每个URL都被正确计数
				if !processedURLs[originalUrl] {
					processedURLs[originalUrl] = true
					localProcessedCount++
				}
				progress := int(float64(localProcessedCount) / float64(totalCount) * 100)
				status := fmt.Sprintf("已完成 %d/%d 个URL的截图", localProcessedCount, totalCount)

				// 发送进度更新和已完成的URL信息（标准化后的URL）
				progressData := map[string]interface{}{
					"progress":     progress,
					"status":       status,
					"completedUrl": normalizeURL(originalUrl),
				}
				jsonData, _ := json.Marshal(progressData)
				fmt.Fprintf(w, "data: %s\n\n", jsonData)

				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
			}
		}()

		// 等待所有截图任务完成
		wg.Wait()
		close(completedURLs) // 确保所有进度更新都已处理

		// 等待一小段时间，确保最后一个进度更新已经发送
		time.Sleep(100 * time.Millisecond)

		// 发送完成通知，确保包含正确的进度文本
		completeData := map[string]interface{}{"progress": 100,
			"status":       fmt.Sprintf("已完成 %d/%d 个URL的截图", totalCount, totalCount),
			"allCompleted": true,
		}
		jsonData, _ := json.Marshal(completeData)
		fmt.Fprintf(w, "data: %s\n\n", jsonData)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}

		fmt.Println("批量截图任务完成")
	})

	// 获取批量截图结果
	http.HandleFunc("/get-batch-screenshots", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// 获取批量截图结果
		batchMutex.Lock()
		// 将二进制数据转换为base64
		base64Screenshots := make(map[string]string)
		for url, imgData := range batchScreenshots {
			base64Screenshots[url] = base64.StdEncoding.EncodeToString(imgData)
		}
		batchMutex.Unlock()

		// 返回base64编码的截图结果
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"screenshots": base64Screenshots,
		})

		fmt.Println("返回批量截图结果")
	})

	// 在后台启动服务器
	go http.Serve(listener, nil)

	return addr
}

// 全局变量用于存储浏览器池 - 增加池大小以提高可靠性
var (
	browserPool      = make(chan context.Context, 10) // 浏览器池，大小为10
	browserPoolMutex sync.Mutex
	allocatorCancels []context.CancelFunc // 存储所有执行分配器的cancel函数
)

// initBrowserPool 初始化浏览器池
func initBrowserPool() {
	// 使用更通用的选项，增强HTTPS和TLS支持，添加跳转处理能力
	// 添加自定义User-Agent以提高截图成功率，避免被识别为爬虫
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		// 基础配置
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", false), // 不禁用GPU以更好地模拟真实浏览器
		chromedp.Flag("ignore-certificate-errors", true),
		chromedp.Flag("ignore-certificate-errors-spki-list", ""),

		// 模拟真实浏览器的User-Agent和配置
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/116.0.0.0 Safari/537.36"),
		chromedp.Flag("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/116.0.0.0 Safari/537.36"),

		// 增强TLS支持
		chromedp.Flag("ssl-version-max", "tls1.3"),
		chromedp.Flag("ssl-version-min", "tls1.2"),
		chromedp.Flag("tls13-ciphersuites", "TLS_AES_256_GCM_SHA384:TLS_CHACHA20_POLY1305_SHA256:TLS_AES_128_GCM_SHA256"),
		// 支持多种TLS曲线
		chromedp.Flag("tls-client-cipher-suites", "ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305"),

		// 启用JavaScript和Web功能
		chromedp.Flag("disable-javascript", false),                       // 启用JavaScript
		chromedp.Flag("enable-javascript", true),                         // 明确启用JavaScript
		chromedp.Flag("enable-webgl", true),                              // 启用WebGL支持
		chromedp.Flag("enable-accelerated-2d-canvas", true),              // 启用加速2D画布
		chromedp.Flag("enable-experimental-web-platform-features", true), // 启用实验性Web平台功能

		// Cookie和存储配置
		chromedp.Flag("disable-features", "SameSiteByDefaultCookies,CookiesWithoutSameSiteMustBeSecure"), // 禁用严格的SameSite策略
		chromedp.Flag("allow-running-insecure-content", true),                                            // 允许运行不安全内容

		// 网络和安全配置
		chromedp.Flag("enable-automation", false),                       // 禁用自动化特征，避免被检测
		chromedp.Flag("disable-blink-features", "AutomationControlled"), // 禁用自动化控制特征
		chromedp.Flag("disable-background-networking", false),           // 启用后台网络

		// 稳定性选项
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("window-size", "1920,1080"), // 设置浏览器窗口大小
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),

		// 性能优化
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-plugins", true),
		chromedp.Flag("disable-popup-blocking", true), // 禁用弹窗拦截
	)

	fmt.Println("开始初始化浏览器池...")

	// 存储所有执行分配器的cancel函数，在程序退出时统一清理
	allocatorCancels = make([]context.CancelFunc, 0, cap(browserPool))

	// 创建5个浏览器实例放入池中
	for i := 0; i < cap(browserPool); i++ {
		// 创建执行分配器
		allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
		allocatorCancels = append(allocatorCancels, cancel)

		// 创建新的上下文
		ctx, _ := chromedp.NewContext(allocCtx)

		browserPool <- ctx
	}

	fmt.Println("浏览器池初始化完成，共", cap(browserPool), "个浏览器实例")
}

// getBrowserContext 从池中获取一个浏览器上下文
// 返回原始池上下文和释放函数，不修改上下文本身
func getBrowserContext() (context.Context, func()) {
	ctx := <-browserPool
	// 返回原始上下文和释放函数
	return ctx, func() {
		// 确保上下文被放回池中，即使有panic发生
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("恢复浏览器上下文时发生panic: %v\n", r)
			}
		}()
		browserPool <- ctx
	}
}

// resetBrowserPool 重置浏览器池，创建新的浏览器实例
// 当切换URL列表或浏览器池出现异常时调用
func resetBrowserPool() {
	fmt.Println("开始重置浏览器池...")

	// 清空并关闭当前浏览器池
	close(browserPool)

	// 清理所有执行分配器
	for _, cancel := range allocatorCancels {
		cancel()
	}

	// 创建新的浏览器池和分配器列表
	browserPool = make(chan context.Context, 10)
	allocatorCancels = make([]context.CancelFunc, 0, cap(browserPool))

	// 重新初始化浏览器池
	initBrowserPool()
	fmt.Println("浏览器池重置完成")
}

// normalizeURL 标准化URL格式，与前端处理逻辑保持一致
func normalizeURL(urlStr string) string {
	// 保留原始URL用于163.com检测
	originalUrl := urlStr

	// 特别处理163.com，确保它能被正确标准化 - 先于斜杠处理，与前端顺序一致
	if strings.Contains(strings.ToLower(originalUrl), "163.com") {
		// 对于所有形式的163.com URL，统一标准化为https://www.163.com
		return "https://www.163.com"
	}

	// 移除路径末尾的斜杠（如果有）- 与前端处理保持一致
	if len(urlStr) > 1 && urlStr[len(urlStr)-1] == '/' {
		urlStr = urlStr[:len(urlStr)-1]
	}

	// 确保其他URL包含协议前缀
	if !strings.HasPrefix(urlStr, "http://") && !strings.HasPrefix(urlStr, "https://") {
		// 默认添加http协议
		urlStr = "http://" + urlStr
	}

	return urlStr
}

// captureScreenshot 捕获指定URL的截图（使用浏览器池）- 增强版支持复杂页面和防爬虫检测
func captureScreenshot(url string, fullPage bool) ([]byte, error) {
	// 标准化URL格式，确保一致性
	url = normalizeURL(url)

	// 存储截图结果
	var buf []byte
	var lastErr error
	maxRetries := 2 // 总共3次尝试

	// 尝试多次截图
	for attempt := 1; attempt <= maxRetries+1; attempt++ {
		// 每次尝试都获取新的浏览器上下文，避免之前的错误影响
		baseCtx, release := getBrowserContext()

		// 计算合理的超时时间，与当前尝试次数相关联
		timeoutDuration := time.Duration(30+(attempt-1)*5) * time.Second

		// 为每次尝试创建新的超时上下文
		ctxWithTimeout, cancel := context.WithTimeout(baseCtx, timeoutDuration)

		fmt.Printf("[尝试 #%d] 开始处理URL: %s\n", attempt, url)

		// 存储最终URL和页面信息
		var finalURL string
		var navigationCompleted bool
		var urlChanged bool
		var documentState string
		var pageContent string

		// 运行任务：导航到URL并等待页面完全加载后再截图
		err := chromedp.Run(ctxWithTimeout,
			// 设置页面加载策略
			chromedp.EmulateViewport(1920, 1080),
			// 导航到URL
			chromedp.Navigate(url),
			// 等待网络空闲，确保大部分资源已加载
			chromedp.WaitNotPresent(`.loading`),
			// 获取页面状态信息
			chromedp.Evaluate(`document.readyState`, &documentState),
			// 使用Text代替不存在的InnerText
			chromedp.Text(`body`, &pageContent, chromedp.ByQuery),
			// 等待页面加载完成，包括跳转
			chromedp.ActionFunc(func(ctx context.Context) error {
				// 检测页面加载状态和URL变化来判断跳转是否完成
				var previousURL = url
				var stableURLCount int

				// 最大等待时间 - 根据总超时调整，确保不超过
				totalWaitTimeUsed := 0 * time.Second
				maxCheckTime := 10 * time.Second // 减少单个检查的时间
				checkInterval := 1000 * time.Millisecond
				maxChecks := int(maxCheckTime / checkInterval)

				for check := 0; check < maxChecks; check++ {
					// 获取当前URL
					currentURL := ""
					if err := chromedp.Evaluate(`window.location.href`, &currentURL).Do(ctx); err != nil {
						fmt.Printf("获取URL失败: %v\n", err)
						continue
					}

					// 检测URL是否稳定（连续2次检查URL相同）
					if currentURL == previousURL {
						stableURLCount++
						if stableURLCount >= 2 {
							finalURL = currentURL
							navigationCompleted = true
							break
						}
					} else {
						stableURLCount = 0
						previousURL = currentURL
						urlChanged = true
					}

					time.Sleep(checkInterval)
					totalWaitTimeUsed += checkInterval

					// 检查是否即将超时
					if remaining := timeoutDuration - totalWaitTimeUsed; remaining < 5*time.Second {
						// 保留足够时间用于后续操作
						break
					}
				}

				// 特别处理：如果页面有加载动画，额外等待但不超时
				timeoutCtx, timeoutCancel := context.WithTimeout(ctx, 3*time.Second)
				if err := chromedp.WaitNotPresent(`.loading-spinner`, chromedp.ByQuery, chromedp.AtLeast(0)).Do(timeoutCtx); err != nil {
					fmt.Printf("等待加载动画消失超时，继续执行\n")
				}
				timeoutCancel()

				// 等待JavaScript执行完成
				timeoutCtx, timeoutCancel = context.WithTimeout(ctx, 3*time.Second)
				if err := chromedp.Evaluate(`new Promise(resolve => setTimeout(resolve, 1000))`, nil).Do(timeoutCtx); err != nil {
					fmt.Printf("等待JavaScript执行超时，继续执行\n")
				}
				timeoutCancel()

				if urlChanged {
					fmt.Printf("[尝试 #%d] URL发生跳转，最终URL: %s\n", attempt, finalURL)
				}

				return nil
			}),
			// 额外的等待时间让页面完全渲染，但限制在总超时内
			chromedp.Sleep(1*time.Second),
			// 截图前滚动页面以确保内容完全加载
			chromedp.ActionFunc(func(ctx context.Context) error {
				// 先滚动到页面底部以触发延迟加载的内容
				if err := chromedp.Evaluate(`window.scrollTo({top: document.body.scrollHeight, behavior: 'smooth'})`, nil).Do(ctx); err != nil {
					fmt.Printf("滚动到页面底部失败: %v\n", err)
				}
				time.Sleep(500 * time.Millisecond) // 等待延迟加载内容
				// 再滚动回顶部，确保从顶部开始截图
				if err := chromedp.Evaluate(`window.scrollTo({top: 0, behavior: 'smooth'})`, nil).Do(ctx); err != nil {
					fmt.Printf("滚动到页面顶部失败: %v\n", err)
				}
				time.Sleep(500 * time.Millisecond) // 等待滚动完成
				return nil
			}),
			// 截图操作 - 提高质量并改进错误处理
			chromedp.ActionFunc(func(ctx context.Context) error {
				fmt.Printf("[尝试 #%d] 准备截图URL: %s\n", attempt, url)
				fmt.Printf("[尝试 #%d] 文档状态: %s, 内容长度: %d字符\n", attempt, documentState, len(pageContent))
				if fullPage {
					return chromedp.FullScreenshot(&buf, 90).Do(ctx) // 提高质量
				} else {
					return chromedp.CaptureScreenshot(&buf).Do(ctx)
				}
			}),
		)

		// 立即取消当前上下文，避免资源泄漏
		cancel()
		// 释放浏览器上下文，立即放回池中
		release()

		// 即使有错误，也检查是否有成功捕获的截图
		if err == nil || len(buf) > 0 {
			if len(buf) > 0 {
				// 截图成功
				fmt.Printf("[尝试 #%d] URL %s 截图成功，大小: %.2f KB\n", attempt, url, float64(len(buf))/1024.0)
				if navigationCompleted && len(finalURL) > 0 && finalURL != url {
					fmt.Printf("[尝试 #%d] 成功处理跳转：从 %s -> %s\n", attempt, url, finalURL)
				}
				return buf, nil
			} else {
				// 没有捕获到截图数据
				lastErr = fmt.Errorf("截图数据为空")
			}
		} else {
			lastErr = err
			fmt.Printf("[尝试 #%d] URL %s 截图失败: %v\n", attempt, url, err)
		}

		// 如果不是最后一次尝试，等待一段时间后再重试
		if attempt <= maxRetries {
			// 增加等待时间以提高重试成功率，使用递增等待策略
			waitTime := time.Duration(attempt*2) * time.Second
			fmt.Printf("[尝试 #%d] 等待%d秒后重试...\n", attempt, waitTime/time.Second)
			time.Sleep(waitTime)
		}
	}

	// 所有尝试都失败
	return nil, fmt.Errorf("执行截图任务失败（已尝试 %d 次）: %v\n可能原因: 网络问题、页面加载失败或防爬虫限制", maxRetries+1, lastErr)
}

// 创建截图失败的占位图
func createErrorPlaceholder(width, height int, url string) []byte {
	// 创建一个简单的SVG作为占位图
	svg := fmt.Sprintf(`<svg width="%d" height="%d" xmlns="http://www.w3.org/2000/svg">
  <rect width="%d" height="%d" fill="#f5f5f5"/>
  <rect x="%d" y="%d" width="200" height="150" fill="#e0e0e0" rx="4"/>
  <text x="%d" y="%d" font-family="Arial" font-size="24" fill="#666666" text-anchor="middle">截图失败</text>
  <text x="%d" y="%d" font-family="Arial" font-size="12" fill="#999999" text-anchor="middle">%s</text>
</svg>`,
		width, height,
		width, height,
		(width-200)/2, (height-150)/2,
		width/2, (height/2)+10,
		width/2, (height/2)+30, url)
	return []byte(svg)
}
