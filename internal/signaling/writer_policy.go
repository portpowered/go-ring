package signaling

// SignalingWriteQueueCapacity bounds writes waiting for the one socket writer.
const SignalingWriteQueueCapacity = 128

// PriorityWriteBurstLimit limits consecutive safety writes while normal work is queued.
// When both queues remain nonempty, at least one normal write runs after this many.
const PriorityWriteBurstLimit = 4
