package discordgo

import "testing"

func TestVoiceCloseClearsOpusChannels(t *testing.T) {
	voice := &VoiceConnection{
		OpusSend: make(chan []byte),
		OpusRecv: make(chan *Packet),
	}

	close(voice.OpusSend)
	close(voice.OpusRecv)

	voice.Close()

	if voice.OpusSend != nil {
		t.Fatal("OpusSend was not cleared")
	}
	if voice.OpusRecv != nil {
		t.Fatal("OpusRecv was not cleared")
	}
}
