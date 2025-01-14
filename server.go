package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Server struct {
	bufferPool sync.Pool
	tlsConfig  *tls.Config
}

func NewServer(certFile, keyFile string) (*Server, error) {
	cert, errLoadX509 := tls.LoadX509KeyPair(certFile, keyFile)
	if errLoadX509 != nil {
		return nil,
			fmt.Errorf("load x509 key pair: %w", errLoadX509)
	}

	return &Server{
			bufferPool: sync.Pool{
				New: func() interface{} {
					return bytes.NewBuffer(nil)
				},
			},

			tlsConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
			},
		},
		nil
}

func (s *Server) Run(address string) error {
	listener, errListen := net.Listen("tcp", address)
	if errListen != nil {
		return fmt.Errorf("listener start: %w", errListen)
	}
	defer listener.Close()

	listenerTLS := tls.NewListener(listener, s.tlsConfig)
	defer listenerTLS.Close()

	log.Printf("Listening on %s (HTTPS)...", address)

	for {
		conn, err := listenerTLS.Accept()
		if err != nil {
			log.Printf("failed to accept connection: %v", err)

			continue
		}

		go s.handleConnection(
			NewConnection(conn),
		)
	}
}

func (s *Server) handleConnection(conn *Connection) {
	defer conn.Close()

	// Set a read deadline for idle connections
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	for {
		if errOnTraffic := s.onTraffic(conn); errOnTraffic != nil {

			break
		}

		// Reset the timeout after successful activity
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	}
}

func (s *Server) SendStatus(statusCode int) []byte {
	buffer := s.bufferPool.Get().(*bytes.Buffer)
	defer s.bufferPool.Put(buffer)
	buffer.Reset()

	buffer.WriteString(fmt.Sprintf("HTTP/1.1 %d %s\r\n", statusCode, http.StatusText(statusCode)))
	buffer.WriteString("Content-Length: 0\r\n")
	buffer.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123))) // Add Date header
	buffer.WriteString("\r\n")

	return buffer.Bytes()
}

func (s *Server) SendBody(statusCode int, body string) []byte {
	buffer := s.bufferPool.Get().(*bytes.Buffer)
	defer s.bufferPool.Put(buffer)
	buffer.Reset()

	buffer.WriteString(fmt.Sprintf("HTTP/1.1 %d %s\r\n", statusCode, http.StatusText(statusCode)))
	buffer.WriteString(fmt.Sprintf("Content-Length: %d\r\n", len(body)))
	buffer.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123))) // Add Date header
	buffer.WriteString("\r\n")
	buffer.WriteString(body)

	return buffer.Bytes()
}

func (s *Server) onTraffic(conn *Connection) error {
	data, errRead := conn.Read()
	if errRead != nil {
		if errRead == io.EOF {
			return nil // client closed the connection gracefully
		}

		return errRead // actual read error
	}

	bufReader := bufio.NewReader(bytes.NewReader(data))

	req, errParse := http.ReadRequest(bufReader)
	if errParse != nil {
		go log.Printf("failed to parse HTTP request: %v", errParse)

		_ = conn.Write([]byte("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n"))

		return nil // Don't close the connection on a bad request
	}

	_ = conn.Write(s.SendStatus(http.StatusOK))

	if strings.ToLower(req.Header.Get("Connection")) == "close" {
		return io.EOF // Signal to close the connection
	}

	return nil // Keep the connection open
}
