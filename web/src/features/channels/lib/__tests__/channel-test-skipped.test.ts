import { describe, expect, test, vi } from 'vitest'

import * as channelApi from '../../api'
import { handleTestChannel } from '../channel-actions'

describe('handleTestChannel skipped handling', () => {
  test('dispatches skipped status and ignores response time on unsupported channels', async () => {
    vi.spyOn(channelApi, 'testChannel').mockResolvedValueOnce({
      success: false,
      status: 'skipped',
      error_code: 'channel_test_unsupported',
      message: 'DoubaoVideo 使用异步任务接口，本次未执行通用测试',
    })

    let callbackSuccess: boolean | undefined
    let callbackResponseTime: number | undefined
    let callbackError: string | undefined
    let callbackErrorCode: string | undefined
    let callbackStatus: string | undefined

    await handleTestChannel(
      54,
      { silent: true, channelName: 'Doubao Video Channel' },
      (success, responseTime, error, errorCode, status) => {
        callbackSuccess = success
        callbackResponseTime = responseTime
        callbackError = error
        callbackErrorCode = errorCode
        callbackStatus = status
      }
    )

    expect(callbackSuccess).toBe(false)
    expect(callbackResponseTime).toBeUndefined()
    expect(callbackStatus).toBe('skipped')
    expect(callbackErrorCode).toBe('channel_test_unsupported')
    expect(callbackError).toContain('未执行通用测试')
  })

  test('preserves normal success flow with response time', async () => {
    vi.spyOn(channelApi, 'testChannel').mockResolvedValueOnce({
      success: true,
      time: 0.123,
      data: {
        response_time: 123,
      },
    })

    let callbackSuccess: boolean | undefined
    let callbackResponseTime: number | undefined
    let callbackStatus: string | undefined

    await handleTestChannel(
      1,
      { silent: true, channelName: 'OpenAI Channel' },
      (success, responseTime, _error, _errorCode, status) => {
        callbackSuccess = success
        callbackResponseTime = responseTime
        callbackStatus = status
      }
    )

    expect(callbackSuccess).toBe(true)
    expect(callbackResponseTime).toBe(123)
    expect(callbackStatus).toBeUndefined()
  })
})
