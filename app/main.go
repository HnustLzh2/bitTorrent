package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

func main() {
	// You can use print statements as follows for debugging, they'll be visible when running tests.
	fmt.Fprintln(os.Stderr, "Logs from your program will appear here!")

	command := os.Args[1]

	switch command {
	case "decode":
		// TODO: Uncomment the code below to pass the first stage
		//
		bencodedValue := os.Args[2]

		decoded, _, err := decodeBencode(bencodedValue)
		if err != nil {
			fmt.Println(err)
			return
		}

		jsonOutput, _ := json.Marshal(decoded)
		fmt.Println(string(jsonOutput))
	case "info":
		torrentFile := os.Args[2]
		response := getInfoFromTorrentFile(torrentFile)
		fmt.Println(response)
	case "peers":
		torrentFile := os.Args[2]
		response, _ := getPeerAddress(torrentFile)
		fmt.Println(response)
	case "handshake":
		torrentFile := os.Args[2]
		address := os.Args[3]
		response := handshake(torrentFile, address)
		fmt.Println(response)
	case "download_piece":
		tag := os.Args[2]
		piecePath := os.Args[3]
		torrentFile := os.Args[4]
		pieceIndex := os.Args[5]
		pieceIndexInt, err := strconv.Atoi(pieceIndex)
		if err != nil {
			fmt.Println("Invalid piece index: " + pieceIndex)
			os.Exit(1)
		}
		_, err = downloadPiece(tag, piecePath, torrentFile, pieceIndexInt)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	case "download":
		// tag := os.Args[2]
		savePath := os.Args[3]
		torrentFile := os.Args[4]
		// err := download(savePath, torrentFile)
		// if err != nil {
		// 	fmt.Println(err)
		// 	os.Exit(1)
		// }
		// 并发版本
		err := downloadFileConcurrent(torrentFile, savePath)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	case "magnet_parse":
		link := os.Args[2]
		decoded, err := decodeMagnetLink(link)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		response := ""
		for k, v := range decoded {
			response += k + ": " + v + "\n"
		}
		fmt.Println(response)
	case "magnet_handshake":
		link := os.Args[2]
		decoded, err := decodeMagnetLink(link)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		_, response, _, _, _, err := magnetHandshake(decoded)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		fmt.Println(response)
	case "magnet_info":
		link := os.Args[2]
		decoded, err := decodeMagnetLink(link)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		response := magnetInfo(decoded)
		fmt.Println(response)
	case "magnet_download_piece":
		// tag := os.Args[2]
		piecePath := os.Args[3]
		magnetLink := os.Args[4]
		pieceIndex := os.Args[5]
		pieceIndexInt, err := strconv.Atoi(pieceIndex)
		if err != nil {
			fmt.Println("Invalid piece index: " + pieceIndex)
			os.Exit(1)
		}
		decodedMap, err := decodeMagnetLink(magnetLink)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		_, err = downloadPieceWithMagnet(piecePath, pieceIndexInt, decodedMap)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	case "magnet_download":
		// tag := os.Args[2]
		filePath := os.Args[3]
		magnetLink := os.Args[4]
		decodedMap, err := decodeMagnetLink(magnetLink)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		err = downloadFileConcurrentWithMagnet(decodedMap, filePath)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	default:
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}
