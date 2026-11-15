import { ref, onUnmounted } from 'vue'
import { attachMicStream, detachMicStream } from '~/composables/useMicLevel'
import { pickRecorderMimeType, micUnavailableReason, describeMicError, withTimeout, dictationAudioConstraints } from '~/utils/micCapture'

export interface VoiceRecordingState {
  isRecording: boolean
  isPaused: boolean
  duration: number
  audioBlob: Blob | null
  error: string | null
}

export function useVoiceRecording() {
  const isRecording = ref(false)
  const isPaused = ref(false)
  const duration = ref(0)
  const audioBlob = ref<Blob | null>(null)
  const error = ref<string | null>(null)

  let mediaRecorder: MediaRecorder | null = null
  let audioChunks: Blob[] = []
  let stream: MediaStream | null = null
  let durationInterval: ReturnType<typeof setInterval> | null = null
  const stopWaiters: Array<(b: Blob | null) => void> = []

  /**
   * Start recording audio from the microphone
   */
  async function startRecording(): Promise<void> {
    try {
      error.value = null
      audioChunks = []
      audioBlob.value = null

      const unavailable = micUnavailableReason()
      if (unavailable) throw new Error(unavailable)

      // Request microphone access
      // WebKit gets unprocessed multi-channel input (see dictationAudioConstraints);
      // transcription mixes the channels down
      stream = await navigator.mediaDevices.getUserMedia({
        audio: dictationAudioConstraints({
          echoCancellation: true,
          noiseSuppression: true,
          sampleRate: 16000 // Whisper prefers 16kHz
        })
      })

      // Safari / the macOS desktop app cannot always record webm: ask which container works
      const mimeType = pickRecorderMimeType()
      mediaRecorder = mimeType ? new MediaRecorder(stream, { mimeType }) : new MediaRecorder(stream)

      // Collect audio data
      mediaRecorder.ondataavailable = (event) => {
        if (event.data.size > 0) {
          audioChunks.push(event.data)
        }
      }

      // Handle recording stop
      mediaRecorder.onstop = () => {
        const blob = new Blob(audioChunks, { type: mediaRecorder?.mimeType || mimeType || 'audio/webm' })
        audioBlob.value = blob
        stopDurationTimer()
        stopWaiters.splice(0).forEach(fn => fn(blob))
      }

      // Handle errors
      mediaRecorder.onerror = (event: Event) => {
        console.error('MediaRecorder error:', event)
        error.value = 'Recording error occurred'
        stopRecording()
      }

      // Start recording
      mediaRecorder.start()
      attachMicStream(stream)
      isRecording.value = true
      startDurationTimer()

    } catch (err) {
      console.error('Failed to start recording:', err)
      error.value = describeMicError(err)
      cleanup()
    }
  }

  /**
   * Stop recording
   */
  function stopRecording(): void {
    if (mediaRecorder && mediaRecorder.state !== 'inactive') {
      mediaRecorder.stop()
    }

    isRecording.value = false
    isPaused.value = false

    // Stop all tracks
    if (stream) {
      detachMicStream(stream)
      stream.getTracks().forEach(track => track.stop())
      stream = null
    }
  }

  /**
   * Stop and wait for the recorder's final data. Safari delivers it later than
   * a fixed delay can cover, so wait for onstop (or give up after 3 s).
   */
  async function stopAndGetBlob(): Promise<Blob | null> {
    const recorder = mediaRecorder
    if (!recorder || recorder.state === 'inactive') {
      stopRecording()
      return audioBlob.value
    }
    const done = new Promise<Blob | null>((resolve) => { stopWaiters.push(resolve) })
    stopRecording()
    return withTimeout(done, 3000, audioBlob.value)
  }

  /**
   * Pause recording
   */
  function pauseRecording(): void {
    if (mediaRecorder && mediaRecorder.state === 'recording') {
      mediaRecorder.pause()
      isPaused.value = true
      stopDurationTimer()
    }
  }

  /**
   * Resume recording
   */
  function resumeRecording(): void {
    if (mediaRecorder && mediaRecorder.state === 'paused') {
      mediaRecorder.resume()
      isPaused.value = false
      startDurationTimer()
    }
  }

  /**
   * Cancel recording (discard audio)
   */
  function cancelRecording(): void {
    stopRecording()
    audioChunks = []
    audioBlob.value = null
    duration.value = 0
  }

  /**
   * Reset state
   */
  function reset(): void {
    audioBlob.value = null
    duration.value = 0
    error.value = null
  }

  /**
   * Start duration timer
   */
  function startDurationTimer(): void {
    durationInterval = setInterval(() => {
      duration.value += 1
    }, 1000)
  }

  /**
   * Stop duration timer
   */
  function stopDurationTimer(): void {
    if (durationInterval) {
      clearInterval(durationInterval)
      durationInterval = null
    }
  }

  /**
   * Cleanup resources
   */
  function cleanup(): void {
    stopRecording()
    stopDurationTimer()
    audioChunks = []
    mediaRecorder = null
  }

  /**
   * Format duration as MM:SS
   */
  function formatDuration(seconds: number): string {
    const mins = Math.floor(seconds / 60)
    const secs = seconds % 60
    return `${mins.toString().padStart(2, '0')}:${secs.toString().padStart(2, '0')}`
  }

  // Cleanup on unmount
  onUnmounted(() => {
    cleanup()
  })

  return {
    // State
    isRecording,
    isPaused,
    duration,
    audioBlob,
    error,

    // Methods
    startRecording,
    stopRecording,
    stopAndGetBlob,
    pauseRecording,
    resumeRecording,
    cancelRecording,
    reset,
    formatDuration
  }
}
