import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { handleBrowserFfmpegExecute } from '../../../../toonflow/runtime/browserMedia'
import { compileFfmpegRequest } from '../../../../toonflow/runtime/ffmpegArgs'
import { vfsReadBytes, vfsRemove, vfsWriteBytes } from '../../../../toonflow/runtime/vfs'

vi.mock('../../../../toonflow/upstream/packages/i18n/src/index', () => ({ translate: (text: string) => text }))

const call = (method: string, ...args: unknown[]) => ({ method, args })
const options = { source: 'assets/input.mp4' }
const audio = [call('output', 'audio.mp3'), call('noVideo'), call('audioCodec', 'libmp3lame'),
  call('audioBitrate', 192), call('format', 'mp3'), call('outputOptions', '-map', '0:a:0')]
const pad = 'pad=ceil(iw/2)*2:ceil(ih/2)*2'
const video = [call('output', 'video.mp4'), call('videoCodec', 'libx264'), call('format', 'mp4'),
  call('videoFilters', [pad]), call('outputOptions', '-map', '0:2', '-preset', 'fast', '-crf', '18', '-pix_fmt', 'yuv420p', '-movflags', '+faststart')]

function expectOptions(args: string[], pairs: [string, string][]) {
  for (const [flag, value] of pairs) {
    expect(args.flatMap((arg, i) => arg === flag ? [args[i + 1]] : []), flag).toContain(value)
  }
}

describe('video node FFmpeg requests', () => {
  it('scopes the audio and video stream maps to their respective split outputs', () => {
    const plan = compileFfmpegRequest(options, [...audio, ...video, call('noAudio')], call('run'), '/job')
    expect(plan).toMatchObject({
      kind: 'exec', inputs: [{ relPath: options.source }], outputs: [{ relPath: 'audio.mp3' }, { relPath: 'video.mp4' }],
    })
    if (plan.kind !== 'exec') throw new Error('expected an execution plan')
    const split = plan.args.indexOf(plan.outputs[0].virtualPath)
    expect(plan.args.slice(0, 2)).toEqual(['-i', plan.inputs[0].virtualPath])
    expect(split).toBeGreaterThan(2)
    expect(plan.args.at(-1)).toBe(plan.outputs[1].virtualPath)
    const audioArgs = plan.args.slice(2, split)
    const videoArgs = plan.args.slice(split + 1, -1)
    expect(audioArgs).toContain('-vn')
    expect(audioArgs).not.toContain('-an')
    expectOptions(audioArgs, [['-c:a', 'libmp3lame'], ['-b:a', '192k'], ['-f', 'mp3'], ['-map', '0:a:0']])
    expect(videoArgs).toContain('-an')
    expect(videoArgs).not.toContain('-vn')
    expectOptions(videoArgs, [
      ['-c:v', 'libx264'], ['-f', 'mp4'], ['-vf', pad], ['-map', '0:2'], ['-preset', 'fast'],
      ['-crf', '18'], ['-pix_fmt', 'yuv420p'], ['-movflags', '+faststart'],
    ])
  })

  it('seeks the input and applies duration and optional audio mapping to the trimmed output', () => {
    const plan = compileFfmpegRequest(options, [call('seekInput', 1.25), ...video, call('duration', 2.5),
      call('audioCodec', 'aac'), call('audioBitrate', 192), call('outputOptions', '-map', '0:a:0?')], call('run'), '/job')
    if (plan.kind !== 'exec') throw new Error('expected an execution plan')
    expect(plan.args.slice(0, 4)).toEqual(['-ss', '1.25', '-i', plan.inputs[0].virtualPath])
    expect(plan.args.at(-1)).toBe(plan.outputs[0].virtualPath)
    expectOptions(plan.args.slice(4, -1), [
      ['-t', '2.5'], ['-c:a', 'aac'], ['-b:a', '192k'], ['-map', '0:a:0?'], ['-map', '0:2'],
    ])
  })
})

it('rejects output aliases that resolve to the source in VFS', () => {
  expect(() => compileFfmpegRequest(options, [call('output', 'assets//./input.mp4 ')], call('run'), '/job'))
    .toThrow(/输出文件不能覆盖原视频/)
})

const directory = '/workspaces/ffmpeg-sse-spec'
const request = { directory, requestId: 'test-job', options }

async function events(operation: string, calls: ReturnType<typeof call>[] = []) {
  const response = await handleBrowserFfmpegExecute({ ...request, calls, operation: call(operation) })
  expect(response.status).toBe(200)
  expect(response.headers.get('content-type')).toContain('text/event-stream')
  const frames = (await response.text()).replace(/\r\n/g, '\n').split('\n\n')
  expect(frames.pop()).toBe('') // The last event must be delimited, not just followed by EOF.
  return frames.map(frame => {
    expect(frame).toMatch(/^data: /)
    return JSON.parse(frame.slice(6)) as { event: string; args: unknown[] }
  })
}

describe('FFmpeg execute SSE', () => {
  // Only the worker reply is synthetic; requests, SSE framing and workspace writes run in production code.
  const reply = vi.fn<(message: { type: string; outputPaths: string[] }) => object>()
  beforeEach(async () => {
    vi.stubGlobal('Worker', class {
      onmessage!: (event: MessageEvent) => void
      terminate() {}
      postMessage(message: Parameters<typeof reply>[0] & { id: number }) {
        this.onmessage(new MessageEvent('message', { data: { id: message.id, ...reply(message) } }))
      }
    })
    await vfsWriteBytes(`${directory}/${options.source}`, Uint8Array.of(1))
  })
  afterEach(async () => {
    await handleBrowserFfmpegExecute({ ...request, operation: call('cancel') })
    vi.unstubAllGlobals()
    reply.mockReset()
    await vfsRemove(directory)
  })

  it('delivers ffprobe metadata as a result frame', async () => {
    const streams = [{ index: 2, codec_type: 'video', width: 320, height: 180 }]
    reply.mockReturnValue({ type: 'ffprobeResult', jsonText: JSON.stringify({ format: { duration: '3.75' }, streams }) })
    expect(await events('ffprobe')).toEqual([{ event: 'result', args: [expect.objectContaining({
      format: expect.objectContaining({ duration: 3.75 }), streams,
    })] }])
    expect(reply).toHaveBeenCalledWith(expect.objectContaining({ type: 'ffprobe' }))
  })

  it('emits end for an audio extraction and persists the returned bytes', async () => {
    const bytes = Uint8Array.of(8, 5, 3)
    reply.mockImplementation(({ outputPaths }) => ({ type: 'execResult',
      outputs: [{ path: outputPaths[0], data: bytes.buffer }], stdout: 'out', stderr: 'log' }))
    const frames = await events('run', audio)
    expect(frames.map(f => f.event)).toEqual(['start', 'end'])
    expect(frames.at(-1)?.args).toEqual(['out', 'log'])
    await expect(vfsReadBytes(`${directory}/audio.mp3`)).resolves.toMatchObject({ bytes, mimeType: 'audio/mpeg' })
  })

  it.each([
    [{ type: 'error', message: 'core load failed' }, /core load failed/],
    [{ type: 'execResult', outputs: [], stdout: '', stderr: '' }, /audio\.mp3/],
  ] as const)('emits error without end or an output file on %j', async (failure, message) => {
    reply.mockReturnValue(failure)
    const frames = await events('run', audio)
    expect(frames.map(f => f.event)).toEqual(['start', 'error'])
    expect(frames.at(-1)?.args).toEqual([expect.objectContaining({ message: expect.stringMatching(message) })])
    await expect(vfsReadBytes(`${directory}/audio.mp3`)).rejects.toMatchObject({ code: 'ENOENT' })
  })
})
