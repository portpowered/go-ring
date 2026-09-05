package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pion/webrtc/v3"
	"github.com/portpowered/go-ring/pkg/ring"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Get access token from environment variable
	accessToken := os.Getenv("RING_ACCESS_TOKEN")
	if accessToken == "" {
		log.Fatal("RING_ACCESS_TOKEN environment variable must be set")
	}

	// Create client with access token
	client, err := ring.NewClientWithToken(accessToken)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Step 1: List devices to select one
	fmt.Println("Step 1: Enumerating devices...")
	devices, err := client.ListDevices(ctx)
	if err != nil {
		log.Fatalf("Failed to list devices: %v", err)
	}

	if devices == nil {
		log.Fatal("Devices response is nil")
	}

	totalDevices := len(devices.Doorbells) + len(devices.Chimes) + len(devices.StickUpCams) + len(devices.Other)
	if totalDevices == 0 {
		log.Fatal("No devices found. Cannot start RTC stream.")
	}

	fmt.Printf("✓ Found %d total device(s)\n", totalDevices)
	fmt.Println()

	// Step 2: Select a device (prefer doorbell or camera for video streaming)
	var deviceID string
	var deviceName string

	if len(devices.Doorbells) > 0 {
		deviceID = devices.Doorbells[0].ID
		deviceName = devices.Doorbells[0].Name
		fmt.Printf("Step 2: Selected device: %s (ID: %s, Type: Doorbell)\n", deviceName, deviceID)
	} else if len(devices.StickUpCams) > 0 {
		deviceID = devices.StickUpCams[0].ID
		deviceName = devices.StickUpCams[0].Name
		fmt.Printf("Step 2: Selected device: %s (ID: %s, Type: StickUp Cam)\n", deviceName, deviceID)
	} else {
		log.Fatal("No doorbells or stickup cams found. These device types support RTC streaming.")
	}
	fmt.Println()

	// Step 3: Create Pion WebRTC peer connection
	fmt.Println("Step 3: Creating WebRTC peer connection...")
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}

	peerConnection, err := webrtc.NewPeerConnection(config)
	if err != nil {
		log.Fatalf("Failed to create peer connection: %v", err)
	}
	defer func() {
		if closeErr := peerConnection.Close(); closeErr != nil {
			log.Printf("Error closing peer connection: %v", closeErr)
		}
	}()

	fmt.Println("✓ Peer connection created")
	fmt.Println()

	// Step 4: Set up video track handler
	fmt.Println("Step 4: Setting up video track handler...")
	videoTrackChan := make(chan *webrtc.TrackRemote, 1)
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		fmt.Printf("✓ Received %s track (ID: %s, SSRC: %d)\n", track.Kind(), track.ID(), track.SSRC())
		select {
		case videoTrackChan <- track:
		default:
		}
	})

	// Monitor connection state
	peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		fmt.Printf("  Connection state changed: %s\n", s.String())
		if s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateClosed {
			fmt.Println("  Connection failed or closed")
		}
	})

	fmt.Println("✓ Track handlers configured")
	fmt.Println()

	// Step 5: Add transceivers for receiving video (required for valid SDP offer)
	fmt.Println("Step 5: Adding video transceiver...")
	_, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})
	if err != nil {
		log.Fatalf("Failed to add video transceiver: %v", err)
	}
	fmt.Println("✓ Video transceiver added (receive-only)")
	fmt.Println()

	// Step 6: Create offer and wait for ICE gathering to complete (non-trickle ICE)
	fmt.Println("Step 6: Creating SDP offer with non-trickle ICE...")

	// Set up ICE candidate handler BEFORE SetLocalDescription to ensure we catch all candidates
	// When candidate is nil, it means gathering is complete
	iceGatheringComplete := make(chan struct{})
	peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			// nil candidate means ICE gathering is complete
			select {
			case <-iceGatheringComplete:
				// Already closed
			default:
				close(iceGatheringComplete)
			}
		} else {
			fmt.Printf("  ICE candidate gathered: %s\n", candidate.ToJSON().Candidate)
		}
	})

	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		log.Fatalf("Failed to create offer: %v", err)
	}

	if err := peerConnection.SetLocalDescription(offer); err != nil {
		log.Fatalf("Failed to set local description: %v", err)
	}

	fmt.Println("  Waiting for ICE gathering to complete (all candidates will be in SDP)...")

	// Wait for ICE gathering with timeout
	select {
	case <-iceGatheringComplete:
		fmt.Println("✓ ICE gathering completed - all candidates included in SDP")
	case <-time.After(30 * time.Second):
		log.Fatal("ICE gathering timeout - failed to gather all candidates within 30 seconds")
	case <-ctx.Done():
		log.Fatal("Context cancelled while waiting for ICE gathering")
	}

	// Get the updated local description with all ICE candidates
	localDesc := peerConnection.LocalDescription()
	if localDesc == nil {
		log.Fatal("Local description is nil after ICE gathering")
	}

	sdpOffer := localDesc.SDP
	candidateCount := strings.Count(sdpOffer, "a=candidate:")
	fmt.Printf("✓ Created complete SDP offer with all ICE candidates (%d bytes, %d candidates)\n", len(sdpOffer), candidateCount)
	fmt.Println()

	// Step 8: Start RTC stream with Ring
	fmt.Printf("Step 8: Starting RTC stream for device %s...\n", deviceName)
	stream, err := client.StartRTCStream(ctx, ring.StartRTCStreamRequest{
		DeviceID: deviceID,
		SDPOffer: sdpOffer,
	})
	if err != nil {
		log.Fatalf("Failed to start RTC stream: %v", err)
	}

	if stream == nil {
		log.Fatal("RTC stream is nil")
	}

	fmt.Printf("✓ RTC stream started successfully!\n")
	fmt.Printf("  Stream ID (Session ID): %s\n", stream.GetStreamID())
	fmt.Printf("  Device ID: %s\n", stream.GetDeviceID())
	fmt.Println()

	// Step 9: Get SDP answer and set it on peer connection
	fmt.Println("Step 9: Setting SDP answer on peer connection...")
	sdpAnswer := stream.GetSDPAnswer()
	if sdpAnswer == "" {
		log.Fatal("SDP answer is empty")
	}

	answer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  sdpAnswer,
	}

	if err := peerConnection.SetRemoteDescription(answer); err != nil {
		log.Fatalf("Failed to set remote description: %v", err)
	}

	fmt.Printf("✓ Set SDP answer (%d bytes)\n", len(sdpAnswer))
	fmt.Println()

	// Step 10: Non-trickle ICE - all candidates are already in the SDP
	fmt.Println("Step 10: Using non-trickle ICE...")
	fmt.Println("  Note: All local ICE candidates were included in the SDP offer")
	fmt.Println("  Note: ICE candidates from Ring are handled internally by the RTCStream")
	fmt.Println("  No separate ICE candidate exchange needed")
	fmt.Println()

	// Step 11: Wait for connection and validate
	fmt.Println("Step 11: Waiting for WebRTC connection to establish...")
	fmt.Println("  Monitoring connection state...")
	fmt.Println("  Press Ctrl+C to stop the stream")
	fmt.Println()

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Monitor connection
	connectionEstablished := false
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sigChan:
			fmt.Println("\nReceived interrupt signal, closing stream...")
			goto cleanup
		case track := <-videoTrackChan:
			if !connectionEstablished {
				fmt.Printf("✓ WebRTC connection established! Receiving %s track\n", track.Kind())
				connectionEstablished = true

				// Start reading from track to validate it's working
				go func() {
					buf := make([]byte, 1500)
					packetCount := 0
					for {
						_, _, err := track.Read(buf)
						if err != nil {
							return
						}
						packetCount++
						if packetCount%100 == 0 {
							fmt.Printf("  Received %d RTP packets from %s track\n", packetCount, track.Kind())
						}
					}
				}()
			}
		case <-ticker.C:
			state := peerConnection.ConnectionState()
			if state == webrtc.PeerConnectionStateConnected && !connectionEstablished {
				fmt.Println("✓ Peer connection state: Connected")
			} else if state == webrtc.PeerConnectionStateFailed {
				fmt.Println("✗ Peer connection failed")
				goto cleanup
			} else if state == webrtc.PeerConnectionStateClosed {
				fmt.Println("  Peer connection closed")
				goto cleanup
			}
		case <-ctx.Done():
			fmt.Println("\nContext cancelled, closing stream...")
			goto cleanup
		}
	}

cleanup:
	// Step 12: Close the stream
	fmt.Println("\nStep 12: Closing RTC stream...")
	err = stream.Close()
	if err != nil {
		log.Printf("Error closing stream: %v", err)
	} else {
		fmt.Println("✓ Stream closed successfully")
	}
	fmt.Println()

	if connectionEstablished {
		fmt.Println("✓ Example completed successfully! WebRTC connection was established and validated.")
	} else {
		fmt.Println("Example completed. Connection may not have fully established.")
	}
}
