import { useEffect, useState } from 'react'
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
  Drawer,
  Descriptions,
  Statistic,
  Row,
  Col,
  Alert,
} from 'antd'
import {
  FileAddOutlined,
  EyeOutlined,
  LockOutlined,
  ReloadOutlined,
} from '@ant-design/icons'
import dayjs from 'dayjs'
import { settlementApi, TeacherSettlement } from '@/services/api'

const statusMap: Record<string, { label: string; color: string }> = {
  pending: { label: '待确认', color: 'default' },
  confirmed: { label: '已确认', color: 'green' },
  needs_recalc: { label: '待重算', color: 'red' },
}

function MonthlySettlement() {
  const [loading, setLoading] = useState(false)
  const [generating, setGenerating] = useState(false)
  const [settlements, setSettlements] = useState<TeacherSettlement[]>([])
  const [month, setMonth] = useState<string>(dayjs().format('YYYY-MM'))
  const [statusFilter, setStatusFilter] = useState<string>('')
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [detail, setDetail] = useState<TeacherSettlement | null>(null)

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
    fetchSettlements()
  }, [month, statusFilter])

  const handleGenerate = async () => {
    try {
      setGenerating(true)
      const res: any = await settlementApi.generate({ month })
      message.success(res?.message || '生成完成')
      fetchSettlements()
    } catch (error) {
      console.error('Generate settlement error:', error)
    } finally {
      setGenerating(false)
    }
  }

  const handleView = async (record: TeacherSettlement) => {
    if (!record.id) return
    try {
      const res: any = await settlementApi.get(record.id)
      setDetail(res)
      setDrawerOpen(true)
    } catch (error) {
      console.error('Fetch settlement detail error:', error)
    }
  }

  const handleConfirm = async (record: TeacherSettlement) => {
    if (!record.id) return
    try {
      await settlementApi.confirm(record.id)
      message.success('已确认，金额已锁定')
      fetchSettlements()
      setDrawerOpen(false)
    } catch (error) {
      console.error('Confirm settlement error:', error)
    }
  }

  const handleRecalc = async (record: TeacherSettlement) => {
    if (!record.id) return
    try {
      await settlementApi.recalculate(record.id)
      message.success('已按最新考勤重算')
      fetchSettlements()
      setDrawerOpen(false)
    } catch (error) {
      console.error('Recalculate settlement error:', error)
    }
  }

  const columns = [
    {
      title: '教师',
      key: 'teacher',
      render: (_: any, record: TeacherSettlement) =>
        record.teacher?.name || `教师#${record.teacher_id}`,
    },
    {
      title: '结算月份',
      dataIndex: 'month',
      key: 'month',
    },
    {
      title: '已点名课时（小时）',
      dataIndex: 'total_hours',
      key: 'total_hours',
    },
    {
      title: '应发工资（元）',
      dataIndex: 'total_salary',
      key: 'total_salary',
      render: (v: number, record: TeacherSettlement) =>
        record.status === 'needs_recalc' ? (
          <span style={{ textDecoration: 'line-through', color: '#999' }}>
            {v?.toFixed(2)}
          </span>
        ) : (
          <strong>{v?.toFixed(2)}</strong>
        ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      render: (status: string) => {
        const s = statusMap[status]
        return <Tag color={s?.color}>{s?.label || status}</Tag>
      },
    },
    {
      title: '操作',
      key: 'action',
      render: (_: any, record: TeacherSettlement) => (
        <Space size="small">
          <Button type="link" size="small" icon={<EyeOutlined />} onClick={() => handleView(record)}>
            查看明细
          </Button>
          {record.status === 'pending' && (
            <Popconfirm
              title="确认后金额将被锁定，确定确认？"
              onConfirm={() => handleConfirm(record)}
              okText="确定"
              cancelText="取消"
            >
              <Button type="link" size="small" icon={<LockOutlined />}>
                财务确认
              </Button>
            </Popconfirm>
          )}
          {record.status === 'needs_recalc' && (
            <Popconfirm
              title="将按最新考勤重新计算，确定重开？"
              onConfirm={() => handleRecalc(record)}
              okText="确定"
              cancelText="取消"
            >
              <Button type="link" size="small" danger icon={<ReloadOutlined />}>
                重新计算
              </Button>
            </Popconfirm>
          )}
        </Space>
      ),
    },
  ]

  const itemColumns = [
    { title: '上课日期', dataIndex: 'date', key: 'date' },
    {
      title: '时间',
      key: 'time',
      render: (_: any, record: any) =>
        `${record.start_time || ''} - ${record.end_time || ''}`,
    },
    { title: '课程', dataIndex: 'course_name', key: 'course_name', render: (v: string) => v || '-' },
    { title: '时长（小时）', dataIndex: 'duration', key: 'duration' },
    {
      title: '课时费标准（元/小时）',
      dataIndex: 'hourly_rate',
      key: 'hourly_rate',
      render: (v: number) => v?.toFixed(2),
    },
    {
      title: '本节课时费（元）',
      dataIndex: 'amount',
      key: 'amount',
      render: (v: number) => <strong>{v?.toFixed(2)}</strong>,
    },
  ]

  return (
    <Card
      title="月度课时费结算"
      extra={
        <Space>
          <DatePicker
            picker="month"
            value={dayjs(month, 'YYYY-MM')}
            onChange={(d) => d && setMonth(d.format('YYYY-MM'))}
            allowClear={false}
          />
          <Select
            style={{ width: 130 }}
            placeholder="状态筛选"
            allowClear
            value={statusFilter || undefined}
            onChange={(v) => setStatusFilter(v || '')}
            options={Object.entries(statusMap).map(([value, s]) => ({
              value,
              label: s.label,
            }))}
          />
          <Popconfirm
            title={`为 ${month} 生成结算单？`}
            description="已有点名记录的教师会生成或重算，已确认的单子只标记待重算"
            onConfirm={handleGenerate}
            okText="生成"
            cancelText="取消"
          >
            <Button type="primary" icon={<FileAddOutlined />} loading={generating}>
              生成月度结算
            </Button>
          </Popconfirm>
        </Space>
      }
    >
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message="仅统计当月已点名（已完成考勤）的课程，未点名的课暂不计入；同一教师同月只有一张结算单，重复生成会按最新考勤重算；已确认的单子金额锁定，补录考勤后会变为“待重算”。"
      />
      <Table
        columns={columns}
        dataSource={settlements}
        rowKey="id"
        loading={loading}
        pagination={false}
        rowClassName={(record) =>
          record.status === 'needs_recalc' ? 'settlement-row-warning' : ''
        }
      />

      <Drawer
        title="结算单明细"
        width={760}
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        extra={
          detail && (
            <Space>
              {detail.status === 'pending' && (
                <Popconfirm
                  title="确认后金额将被锁定，确定确认？"
                  onConfirm={() => handleConfirm(detail)}
                  okText="确定"
                  cancelText="取消"
                >
                  <Button type="primary" icon={<LockOutlined />}>财务确认</Button>
                </Popconfirm>
              )}
              {detail.status === 'needs_recalc' && (
                <Popconfirm
                  title="将按最新考勤重新计算，确定重开？"
                  onConfirm={() => handleRecalc(detail)}
                  okText="确定"
                  cancelText="取消"
                >
                  <Button danger icon={<ReloadOutlined />}>重新计算</Button>
                </Popconfirm>
              )}
            </Space>
          )
        }
      >
        {detail && (
          <>
            {detail.status === 'needs_recalc' && (
              <Alert
                type="warning"
                showIcon
                style={{ marginBottom: 16 }}
                message="该单已被财务确认并锁定，之后又有新的考勤补录。显示金额为锁定金额，请点击“重新计算”按最新考勤重开。"
              />
            )}
            <Descriptions column={2} bordered size="small" style={{ marginBottom: 16 }}>
              <Descriptions.Item label="教师">
                {detail.teacher?.name || `教师#${detail.teacher_id}`}
              </Descriptions.Item>
              <Descriptions.Item label="结算月份">{detail.month}</Descriptions.Item>
              <Descriptions.Item label="状态">
                <Tag color={statusMap[detail.status]?.color}>
                  {statusMap[detail.status]?.label || detail.status}
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label="确认时间">
                {detail.confirmed_at
                  ? dayjs(detail.confirmed_at).format('YYYY-MM-DD HH:mm')
                  : '-'}
              </Descriptions.Item>
            </Descriptions>
            <Row gutter={16} style={{ marginBottom: 16 }}>
              <Col span={12}>
                <Card>
                  <Statistic title="已点名课时（小时）" value={detail.total_hours} />
                </Card>
              </Col>
              <Col span={12}>
                <Card>
                  <Statistic
                    title="应发工资（元）"
                    value={detail.total_salary}
                    precision={2}
                    prefix="¥"
                  />
                </Card>
              </Col>
            </Row>
            <Table
              columns={itemColumns}
              dataSource={detail.items || []}
              rowKey="id"
              size="small"
              pagination={false}
            />
          </>
        )}
      </Drawer>
    </Card>
  )
}

export default MonthlySettlement
