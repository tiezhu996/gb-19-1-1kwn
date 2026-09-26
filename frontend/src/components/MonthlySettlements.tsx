import { useEffect, useMemo, useState } from 'react'
import dayjs from 'dayjs'
import {
  Table,
  Card,
  Button,
  Space,
  Popconfirm,
  message,
  DatePicker,
  Select,
  Tag,
  Modal,
  Descriptions,
  Statistic,
  Row,
  Col,
  Empty,
} from 'antd'
import {
  FileAddOutlined,
  EyeOutlined,
  LockOutlined,
  ReloadOutlined,
} from '@ant-design/icons'
import {
  settlementApi,
  teacherApi,
  Settlement,
  SettlementItem,
  SettlementStatus,
} from '@/services/api'

const statusMeta: Record<
  SettlementStatus,
  { label: string; color: string; tip?: string }
> = {
  pending: { label: '待确认', color: 'default' },
  confirmed: { label: '已确认', color: 'success', tip: '金额已锁定' },
  stale: { label: '待重算', color: 'warning', tip: '确认后考勤有补录/改动，需要重开重算' },
}

function MonthlySettlements() {
  const [month, setMonth] = useState(dayjs().format('YYYY-MM'))
  const [statusFilter, setStatusFilter] = useState<string>('')
  const [loading, setLoading] = useState(false)
  const [generating, setGenerating] = useState(false)
  const [settlements, setSettlements] = useState<Settlement[]>([])
  const [teacherMap, setTeacherMap] = useState<Record<number, string>>({})

  // 详情弹窗
  const [detailOpen, setDetailOpen] = useState(false)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detail, setDetail] = useState<Settlement | null>(null)

  const fetchTeachers = async () => {
    try {
      const res: any = await teacherApi.list({ page_size: 1000 })
      const map: Record<number, string> = {}
      ;(res.list || []).forEach((t: any) => {
        map[t.id] = t.name
      })
      setTeacherMap(map)
    } catch (error) {
      console.error('Fetch teachers error:', error)
    }
  }

  const fetchSettlements = async () => {
    try {
      setLoading(true)
      const params: any = { month }
      if (statusFilter) {
        params.status = statusFilter
      }
      const res: any = await settlementApi.list(params)
      setSettlements(res || [])
    } catch (error) {
      console.error('Fetch settlements error:', error)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchTeachers()
  }, [])

  useEffect(() => {
    fetchSettlements()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [month, statusFilter])

  const handleGenerate = async () => {
    try {
      setGenerating(true)
      const res: any = await settlementApi.generate(month)
      const generatedCount = res.generated?.length || 0
      const skippedList = res.skipped || []
      const lockedCount = skippedList.filter((s: any) => s.status === 'confirmed').length
      const staleSkippedCount = skippedList.filter((s: any) => s.status === 'stale').length
      if (generatedCount > 0) {
        message.success(
          `${month} 已生成/重算 ${generatedCount} 张结算单` +
            (skippedList.length > 0
              ? `，${lockedCount + staleSkippedCount} 张锁定的单子未改动`
              : ''),
        )
      } else if (skippedList.length > 0) {
        message.info(
          `本月 ${lockedCount} 张已确认、${staleSkippedCount} 张待重算的单子保持锁定，请逐张重开重算`,
        )
      } else {
        message.info('本月还没有点过名的课，未生成结算单')
      }
      fetchSettlements()
    } catch (error) {
      console.error('Generate settlements error:', error)
    } finally {
      setGenerating(false)
    }
  }

  const handleView = async (id: number) => {
    try {
      setDetailOpen(true)
      setDetailLoading(true)
      setDetail(null)
      const res: any = await settlementApi.get(id)
      setDetail(res)
    } catch (error) {
      console.error('Fetch settlement detail error:', error)
      setDetailOpen(false)
    } finally {
      setDetailLoading(false)
    }
  }

  const handleConfirm = async (id: number) => {
    try {
      await settlementApi.confirm(id)
      message.success('已确认，金额锁定')
      fetchSettlements()
      if (detail?.id === id) {
        handleView(id)
      }
    } catch (error) {
      console.error('Confirm settlement error:', error)
    }
  }

  const handleReopen = async (id: number) => {
    try {
      await settlementApi.reopen(id)
      message.success('已按最新考勤重开重算')
      fetchSettlements()
      if (detail?.id === id && detailOpen) {
        handleView(id)
      } else {
        setDetailOpen(false)
      }
    } catch (error) {
      console.error('Reopen settlement error:', error)
    }
  }

  const detailColumns = [
    {
      title: '上课日期',
      dataIndex: 'date',
      key: 'date',
    },
    {
      title: '时间',
      key: 'time',
      render: (_: any, record: SettlementItem) =>
        record.start_time && record.end_time
          ? `${record.start_time} - ${record.end_time}`
          : '-',
    },
    {
      title: '课程',
      dataIndex: 'course_name',
      key: 'course_name',
      render: (name: string) => name || '-',
    },
    {
      title: '时长(小时)',
      dataIndex: 'duration',
      key: 'duration',
    },
    {
      title: '课时费标准(元/小时)',
      dataIndex: 'hourly_rate',
      key: 'hourly_rate',
    },
    {
      title: '本节课时费(元)',
      dataIndex: 'amount',
      key: 'amount',
      render: (amount: number) => <strong>{Number(amount || 0).toFixed(2)}</strong>,
    },
  ]

  const columns = [
    {
      title: '教师',
      key: 'teacher',
      render: (_: any, record: Settlement) =>
        record.teacher?.name || teacherMap[record.teacher_id] || `教师#${record.teacher_id}`,
    },
    {
      title: '结算月份',
      dataIndex: 'month',
      key: 'month',
    },
    {
      title: '已点名课时(小时)',
      dataIndex: 'total_hours',
      key: 'total_hours',
    },
    {
      title: '课时费标准(元/小时)',
      dataIndex: 'hourly_rate',
      key: 'hourly_rate',
    },
    {
      title: '应发工资(元)',
      dataIndex: 'total_amount',
      key: 'total_amount',
      render: (amount: number) => (
        <strong style={{ fontSize: 15 }}>{Number(amount || 0).toFixed(2)}</strong>
      ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      render: (status: SettlementStatus) => <TooltipMeta status={status} />,
    },
    {
      title: '操作',
      key: 'action',
      render: (_: any, record: Settlement) => (
        <Space size="small">
          <Button
            type="link"
            size="small"
            icon={<EyeOutlined />}
            onClick={() => handleView(record.id!)}
          >
            查看明细
          </Button>
          {record.status === 'pending' && (
            <Popconfirm
              title="确认后金额将锁定"
              description="之后补录的考勤只会标记为待重算"
              onConfirm={() => handleConfirm(record.id!)}
              okText="确认锁定"
              cancelText="取消"
            >
              <Button type="link" size="small" icon={<LockOutlined />}>
                财务确认
              </Button>
            </Popconfirm>
          )}
          {(record.status === 'stale' || record.status === 'confirmed') && (
            <Popconfirm
              title="按最新考勤重开重算？"
              description={
                record.status === 'stale'
                  ? '将解锁已确认金额，按补录后的考勤重新计算'
                  : '将解锁已确认金额并重新计算'
              }
              onConfirm={() => handleReopen(record.id!)}
              okText="重开重算"
              cancelText="取消"
            >
              <Button
                type="link"
                size="small"
                danger={record.status === 'stale'}
                icon={<ReloadOutlined />}
              >
                重开重算
              </Button>
            </Popconfirm>
          )}
        </Space>
      ),
    },
  ]

  const staleCount = useMemo(
    () => settlements.filter((s) => s.status === 'stale').length,
    [settlements],
  )

  return (
    <div>
      <Card
        title="月度课时费结算"
        extra={
          <Space>
            <DatePicker
              picker="month"
              value={dayjs(month, 'YYYY-MM')}
              onChange={(d) => setMonth(d ? d.format('YYYY-MM') : dayjs().format('YYYY-MM'))}
              allowClear={false}
            />
            <Select
              style={{ width: 130 }}
              placeholder="全部状态"
              allowClear
              value={statusFilter || undefined}
              onChange={(v) => setStatusFilter(v || '')}
              options={[
                { value: 'pending', label: '待确认' },
                { value: 'confirmed', label: '已确认' },
                { value: 'stale', label: '待重算' },
              ]}
            />
            <Button
              type="primary"
              icon={<FileAddOutlined />}
              loading={generating}
              onClick={handleGenerate}
            >
              生成/重算 {month} 结算单
            </Button>
          </Space>
        }
      >
        {staleCount > 0 && (
          <div style={{ marginBottom: 12 }}>
            <Tag color="warning" icon={<ReloadOutlined />}>
              {staleCount} 位教师的结算单因考勤补录/改动需要重开重算
            </Tag>
          </div>
        )}
        <Table
          columns={columns}
          dataSource={settlements}
          rowKey="id"
          loading={loading}
          pagination={false}
          locale={{
            emptyText: (
              <Empty
                description={`${month} 暂无结算单，点右上角按钮按当月已点名的课生成`}
              />
            ),
          }}
        />
      </Card>

      <Modal
        title={
          detail
            ? `${detail.teacher?.name || teacherMap[detail.teacher_id] || '教师'} ${detail.month} 课时费结算单`
            : '结算单详情'
        }
        open={detailOpen}
        onCancel={() => setDetailOpen(false)}
        footer={[
          <Button key="close" onClick={() => setDetailOpen(false)}>
            关闭
          </Button>,
          detail?.status === 'pending' && (
            <Popconfirm
              key="confirm"
              title="确认后金额将锁定"
              onConfirm={() => handleConfirm(detail.id!)}
              okText="确认锁定"
              cancelText="取消"
            >
              <Button type="primary" icon={<LockOutlined />}>
                财务确认
              </Button>
            </Popconfirm>
          ),
          (detail?.status === 'stale' || detail?.status === 'confirmed') && (
            <Popconfirm
              key="reopen"
              title="按最新考勤重开重算？"
              onConfirm={() => handleReopen(detail.id!)}
              okText="重开重算"
              cancelText="取消"
            >
              <Button type="primary" danger icon={<ReloadOutlined />}>
                重开重算
              </Button>
            </Popconfirm>
          ),
        ]}
        width={860}
      >
        {detailLoading || !detail ? (
          <div style={{ textAlign: 'center', padding: 40 }}>加载中...</div>
        ) : (
          <>
            <Row gutter={16} style={{ marginBottom: 16 }}>
              <Col span={6}>
                <Statistic
                  title="已点名课时(小时)"
                  value={Number(detail.total_hours || 0)}
                  precision={2}
                />
              </Col>
              <Col span={6}>
                <Statistic
                  title="课时费标准(元/小时)"
                  value={Number(detail.hourly_rate || 0)}
                  precision={2}
                />
              </Col>
              <Col span={6}>
                <Statistic
                  title="应发工资(元)"
                  value={Number(detail.total_amount || 0)}
                  precision={2}
                  valueStyle={{ color: '#cf1322' }}
                />
              </Col>
              <Col span={6}>
                <div style={{ paddingTop: 4 }}>
                  <div style={{ color: 'rgba(0,0,0,0.45)', fontSize: 14 }}>状态</div>
                  <div style={{ paddingTop: 8 }}>
                    <TooltipMeta status={detail.status} />
                  </div>
                </div>
              </Col>
            </Row>
            <Descriptions size="small" style={{ marginBottom: 12 }}>
              <Descriptions.Item label="说明">
                仅统计当月已点名的课，未点名的课不计入
              </Descriptions.Item>
            </Descriptions>
            <Table
              columns={detailColumns}
              dataSource={detail.items || []}
              rowKey="id"
              size="small"
              pagination={false}
              summary={(pageData) => {
                const total = pageData.reduce((sum, it) => sum + Number(it.amount || 0), 0)
                return (
                  <Table.Summary.Row>
                    <Table.Summary.Cell index={0} colSpan={5}>
                      <strong>合计</strong>
                    </Table.Summary.Cell>
                    <Table.Summary.Cell index={5}>
                      <strong>{total.toFixed(2)} 元</strong>
                    </Table.Summary.Cell>
                  </Table.Summary.Row>
                )
              }}
            />
          </>
        )}
      </Modal>
    </div>
  )
}

function TooltipMeta({ status }: { status: SettlementStatus }) {
  const meta = statusMeta[status] || { label: status, color: 'default' }
  if (status === 'stale') {
    return (
      <Tag color={meta.color}>
        {meta.label}：考勤有补录，需重开重算
      </Tag>
    )
  }
  return <Tag color={meta.color}>{meta.label}</Tag>
}

export default MonthlySettlements
