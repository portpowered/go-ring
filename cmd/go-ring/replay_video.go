package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

func replayVideoCommand(args []string, out io.Writer) error {
	if len(args) < 1 {
		return errors.New("replay-video requires an RTP recording")
	}
	flags := flag.NewFlagSet("replay-video", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	outputPath := flags.String("output", "", "Annex B H264 output")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *outputPath == "" || flags.NArg() != 0 {
		return errors.New("usage: replay-video <recording> --output file.h264")
	}
	input, err := os.Open(args[0])
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(*outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateFileMode)
	if err != nil {
		return err
	}
	defer output.Close()
	if err := output.Chmod(privateFileMode); err != nil {
		_ = output.Close()
		_ = os.Remove(*outputPath)
		return err
	}
	if err := restrictTokenFile(*outputPath); err != nil {
		_ = output.Close()
		_ = os.Remove(*outputPath)
		return err
	}
	writer := newH264FrameWriter(output)
	if err := replayRTP(input, writer.WriteRTP); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Saved %s (H264 video from recorded RTP)\n", *outputPath)
	return err
}
