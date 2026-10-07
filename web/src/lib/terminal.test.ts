import { describe, expect, it, vi } from 'vitest'
import { sendTerminalInput, sendTerminalResize, sendTerminalScroll, sendTerminalRelease } from './terminal'
const socket = () => ({ readyState:1, send:vi.fn() })
describe('terminal protocol controls', () => {
  it('never sends input from observe mode', () => {
    const ws = socket()
    sendTerminalInput(ws, 'observe', 'secret')
    expect(ws.send).not.toHaveBeenCalled()
  })
  it('does not send resize or scroll while observing', () => {
    const ws = socket()
    sendTerminalResize(ws, 'observe', 100, 30)
    sendTerminalScroll(ws, 'observe', 8)
    expect(ws.send).not.toHaveBeenCalled()
  })
  it('bounds resize and sends release before disconnect', () => {
    const ws = socket()
    sendTerminalResize(ws, 'control', 9000, 0)
    sendTerminalScroll(ws, 'control', 8)
    sendTerminalRelease(ws)
    expect(ws.send).toHaveBeenNthCalledWith(1, JSON.stringify({type:'resize',cols:500,rows:5}))
    expect(ws.send).toHaveBeenNthCalledWith(2, JSON.stringify({type:'scroll',delta:8}))
    expect(ws.send).toHaveBeenNthCalledWith(3, JSON.stringify({type:'release'}))
  })
})
