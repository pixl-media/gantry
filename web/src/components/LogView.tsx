import ClearAllIcon from '@mui/icons-material/ClearAll';
import DownloadIcon from '@mui/icons-material/Download';
import KeyboardDoubleArrowDownIcon from '@mui/icons-material/KeyboardDoubleArrowDown';
import SearchIcon from '@mui/icons-material/Search';
import {
  Alert,
  Box,
  Button,
  Checkbox,
  Chip,
  FormControlLabel,
  IconButton,
  InputAdornment,
  ListItemText,
  MenuItem,
  Select,
  Stack,
  Switch,
  TextField,
  Tooltip,
  Typography,
} from '@mui/material';
import {
  memo,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { useTranslation } from 'react-i18next';
import { useStackLogs, type KeyedLogLine } from '../api/logs';
import { parseAnsi, stripAnsi } from '../lib/ansi';
import { monoFont } from '../theme';

// Must include the default (200), or the select shows no value.
const tailOptions = [100, 200, 500, 2000] as const;

interface Props {
  stack: string;
  services: string[];
  /** Show only this container (set from a container's logs button). */
  container?: string;
  onContainerChange: (container: string | undefined) => void;
  /** For tests: the most lines kept. */
  maxLines?: number;
}

/** A stack's logs, merged and live, with filters and search. */
export function LogView({
  stack,
  services,
  container,
  onContainerChange,
  maxLines,
}: Props) {
  const { t, i18n } = useTranslation();
  const [tail, setTail] = useState<number>(200);
  const { lines, reconnecting, failed, clear } = useStackLogs(
    stack,
    tail,
    maxLines,
  );
  const [selected, setSelected] = useState<string[]>([]);
  const [query, setQuery] = useState('');
  const [timestamps, setTimestamps] = useState(true);
  const [atBottom, setAtBottom] = useState(true);
  const scrollRef = useRef<HTMLDivElement>(null);

  const q = query.trim().toLowerCase();
  const visible = useMemo(
    () =>
      lines.filter(
        (l) =>
          (selected.length === 0 || selected.includes(l.service)) &&
          (!container || l.container === container) &&
          (!q || stripAnsi(l.line).toLowerCase().includes(q)),
      ),
    [lines, selected, container, q],
  );

  // Follow new lines while the user is at the bottom.
  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (el && atBottom) el.scrollTop = el.scrollHeight;
  }, [visible, atBottom]);

  const timeFormat = useMemo(
    () =>
      new Intl.DateTimeFormat(i18n.language, {
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        fractionalSecondDigits: 3,
        hour12: false,
      }),
    [i18n.language],
  );

  const download = () => {
    const text = visible
      .map((l) => `${l.time} ${l.service} | ${stripAnsi(l.line)}`)
      .join('\n');
    const url = URL.createObjectURL(
      new Blob([text + '\n'], { type: 'text/plain' }),
    );
    const a = document.createElement('a');
    a.href = url;
    a.download = `${stack}-logs-${new Date().toISOString().slice(0, 19)}.txt`;
    a.click();
    URL.revokeObjectURL(url);
  };

  return (
    <Stack sx={{ gap: 1.5 }}>
      <Stack
        direction="row"
        sx={{ gap: 1.5, alignItems: 'center', flexWrap: 'wrap' }}
      >
        <Select
          multiple
          size="small"
          displayEmpty
          value={selected}
          onChange={(e) =>
            setSelected(
              typeof e.target.value === 'string'
                ? e.target.value.split(',')
                : e.target.value,
            )
          }
          renderValue={(v) =>
            v.length === 0 ? t('logs.allServices') : v.join(', ')
          }
          inputProps={{ 'aria-label': t('logs.services') }}
          sx={{ minWidth: 180 }}
        >
          {services.map((s) => (
            <MenuItem key={s} value={s}>
              <Checkbox size="small" checked={selected.includes(s)} />
              <ListItemText primary={s} />
            </MenuItem>
          ))}
        </Select>
        {container && (
          <Chip
            label={t('logs.containerFilter', { name: container })}
            onDelete={() => onContainerChange(undefined)}
          />
        )}
        <TextField
          size="small"
          placeholder={t('logs.search')}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          slotProps={{
            htmlInput: { 'aria-label': t('logs.search') },
            input: {
              startAdornment: (
                <InputAdornment position="start">
                  <SearchIcon fontSize="small" />
                </InputAdornment>
              ),
            },
          }}
          sx={{ minWidth: 200, flex: 1 }}
        />
        <Select
          size="small"
          value={tail}
          onChange={(e) => setTail(Number(e.target.value))}
          inputProps={{ 'aria-label': t('logs.tail') }}
        >
          {tailOptions.map((n) => (
            <MenuItem key={n} value={n}>
              {t('logs.tailOption', { count: n })}
            </MenuItem>
          ))}
        </Select>
        <FormControlLabel
          control={
            <Switch
              size="small"
              checked={timestamps}
              onChange={(e) => setTimestamps(e.target.checked)}
            />
          }
          label={t('logs.timestamps')}
        />
        <Tooltip title={t('logs.clear')}>
          <IconButton aria-label={t('logs.clear')} onClick={clear}>
            <ClearAllIcon />
          </IconButton>
        </Tooltip>
        <Tooltip title={t('logs.download')}>
          <span>
            <IconButton
              aria-label={t('logs.download')}
              onClick={download}
              disabled={visible.length === 0}
            >
              <DownloadIcon />
            </IconButton>
          </span>
        </Tooltip>
      </Stack>

      {failed && (
        <Alert severity="error">{t('logs.failed', { message: failed })}</Alert>
      )}
      {reconnecting && <Alert severity="info">{t('logs.reconnecting')}</Alert>}

      <Box sx={{ position: 'relative' }}>
        <Box
          ref={scrollRef}
          data-testid="log-lines"
          onScroll={(e) => {
            const el = e.currentTarget;
            setAtBottom(el.scrollHeight - el.scrollTop - el.clientHeight < 24);
          }}
          sx={{
            height: { xs: '60vh', md: 'calc(100vh - 360px)' },
            minHeight: 320,
            overflow: 'auto',
            borderRadius: 1,
            bgcolor: 'grey.900',
            color: 'grey.100',
            fontFamily: monoFont,
            fontSize: 12.5,
            lineHeight: 1.6,
            py: 1,
          }}
        >
          {visible.length === 0 ? (
            <Typography
              sx={{ px: 1.5, color: 'grey.500', fontSize: 'inherit' }}
            >
              {lines.length === 0 ? t('logs.empty') : t('logs.noMatches')}
            </Typography>
          ) : (
            visible.map((l) => (
              <LogRow
                key={l.key}
                line={l}
                time={timestamps ? timeFormat.format(new Date(l.time)) : ''}
                query={q}
              />
            ))
          )}
        </Box>
        {!atBottom && (
          <Button
            variant="contained"
            size="small"
            startIcon={<KeyboardDoubleArrowDownIcon />}
            onClick={() => setAtBottom(true)}
            sx={{ position: 'absolute', right: 16, bottom: 16 }}
          >
            {t('logs.jumpToLatest')}
          </Button>
        )}
      </Box>
      <Typography variant="caption" color="text.secondary">
        {t('logs.count', { count: visible.length })}
      </Typography>
    </Stack>
  );
}

/** A stable, readable color per service name (on the dark log background). */
export function serviceColor(service: string): string {
  let hash = 0;
  for (const ch of service) hash = (hash * 31 + ch.charCodeAt(0)) % 360;
  return `hsl(${hash}, 65%, 68%)`;
}

const LogRow = memo(function LogRow({
  line,
  time,
  query,
}: {
  line: KeyedLogLine;
  time: string;
  query: string;
}) {
  const stderr = line.stream === 'stderr';
  return (
    <Box
      data-stream={line.stream}
      sx={{
        display: 'flex',
        gap: 1.5,
        px: 1.5,
        borderLeft: 3,
        borderColor: stderr ? 'error.main' : 'transparent',
        '&:hover': { bgcolor: 'rgba(255,255,255,0.04)' },
      }}
    >
      {time && (
        <Box component="span" sx={{ color: 'grey.500', flexShrink: 0 }}>
          {time}
        </Box>
      )}
      <Box
        component="span"
        title={line.container}
        sx={{
          color: serviceColor(line.service),
          flexShrink: 0,
          minWidth: '8ch',
        }}
      >
        {line.service}
      </Box>
      <Box
        component="span"
        sx={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', minWidth: 0 }}
      >
        {parseAnsi(line.line).map((seg, i) => (
          <span
            key={i}
            style={{
              color: seg.style.color,
              backgroundColor: seg.style.background,
              fontWeight: seg.style.bold ? 700 : undefined,
              opacity: seg.style.dim ? 0.7 : undefined,
              fontStyle: seg.style.italic ? 'italic' : undefined,
              textDecoration: seg.style.underline ? 'underline' : undefined,
            }}
          >
            {highlight(seg.text, query)}
          </span>
        ))}
      </Box>
    </Box>
  );
});

/** Marks case-insensitive matches of query in text. */
function highlight(text: string, query: string): ReactNode {
  if (!query) return text;
  const lower = text.toLowerCase();
  const parts: ReactNode[] = [];
  let from = 0;
  for (let i = lower.indexOf(query); i >= 0; i = lower.indexOf(query, from)) {
    parts.push(text.slice(from, i));
    parts.push(
      <mark key={i} style={{ backgroundColor: '#ffcc66', color: '#000' }}>
        {text.slice(i, i + query.length)}
      </mark>,
    );
    from = i + query.length;
  }
  parts.push(text.slice(from));
  return parts;
}
