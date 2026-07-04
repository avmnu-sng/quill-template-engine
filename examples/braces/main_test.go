package main

func Example() {
	if err := render(); err != nil {
		panic(err)
	}
	// Output:
	// server {
	// 	listen 80;
	// 	server_name example.com;
	// 	location / {
	// 		proxy_pass http://127.0.0.1:8080;
	// 	}
	// 	location /api {
	// 		proxy_pass http://127.0.0.1:9090;
	// 	}
	// 	location /static {
	// 		proxy_pass http://127.0.0.1:7070;
	// 	}
	// }
}
