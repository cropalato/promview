//! Parsing a Server-Sent Events byte stream.
//!
//! Kept apart from the transport so it can be tested against bytes rather than
//! a socket. The subset is what the Promview stream actually sends: named
//! events, an id, and a single data line per frame. Comments and keepalives are
//! consumed and produce nothing, which is the point of them.

/// One dispatched SSE frame.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SseEvent {
    pub event: String,
    pub data: String,
    pub id: Option<String>,
}

/// Accumulates bytes and yields frames as their blank-line terminators arrive.
///
/// A chunked response splits wherever the network felt like it, so a frame can
/// arrive in pieces and two frames can arrive together. Holding the remainder
/// between calls is the whole job.
#[derive(Debug, Default)]
pub struct SseParser {
    buffer: String,
    event: String,
    data: Vec<String>,
    id: Option<String>,
}

impl SseParser {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn push(&mut self, chunk: &str) -> Vec<SseEvent> {
        self.buffer.push_str(chunk);
        let mut events = Vec::new();
        // Only complete lines are processed; whatever follows the last newline
        // stays buffered for the next chunk.
        while let Some(position) = self.buffer.find('\n') {
            let line: String = self.buffer.drain(..=position).collect();
            let line = line.trim_end_matches('\n').trim_end_matches('\r');
            if let Some(event) = self.line(line) {
                events.push(event);
            }
        }
        events
    }

    fn line(&mut self, line: &str) -> Option<SseEvent> {
        if line.is_empty() {
            return self.dispatch();
        }
        if line.starts_with(':') {
            // A comment. Promview uses these as keepalives; they mean the
            // connection is alive and nothing else.
            return None;
        }
        let (field, value) = match line.split_once(':') {
            Some((field, value)) => (field, value.strip_prefix(' ').unwrap_or(value)),
            None => (line, ""),
        };
        match field {
            "event" => self.event = value.to_string(),
            "data" => self.data.push(value.to_string()),
            "id" => self.id = Some(value.to_string()),
            // "retry" and anything unknown are ignored: reconnect timing is the
            // console's policy, and it already has one.
            _ => {}
        }
        None
    }

    fn dispatch(&mut self) -> Option<SseEvent> {
        if self.data.is_empty() && self.event.is_empty() {
            // A blank line with nothing before it: a keepalive terminator.
            return None;
        }
        let event = SseEvent {
            event: std::mem::take(&mut self.event),
            data: self.data.join("\n"),
            id: self.id.clone(),
        };
        self.data.clear();
        Some(event)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_a_complete_frame() {
        let mut parser = SseParser::new();
        let events = parser.push("event: alert.created\nid: 42\ndata: {\"a\":1}\n\n");
        assert_eq!(
            events,
            vec![SseEvent {
                event: "alert.created".to_string(),
                data: "{\"a\":1}".to_string(),
                id: Some("42".to_string()),
            }]
        );
    }

    #[test]
    fn reassembles_a_frame_split_across_chunks() {
        // The network splits where it likes; a frame arriving in pieces is the
        // normal case, not the exception.
        let mut parser = SseParser::new();
        assert!(parser.push("event: alert.up").is_empty());
        assert!(parser.push("dated\ndata: {\"b\"").is_empty());
        let events = parser.push(":2}\n\n");
        assert_eq!(events.len(), 1);
        assert_eq!(events[0].event, "alert.updated");
        assert_eq!(events[0].data, "{\"b\":2}");
    }

    #[test]
    fn yields_two_frames_that_arrived_together() {
        let mut parser = SseParser::new();
        let events = parser.push("event: a\ndata: 1\n\nevent: b\ndata: 2\n\n");
        assert_eq!(events.len(), 2);
        assert_eq!(events[1].event, "b");
    }

    #[test]
    fn swallows_comments_and_keepalives() {
        let mut parser = SseParser::new();
        assert!(parser.push(": keepalive\n\n").is_empty());
        assert!(parser.push("\n").is_empty());
    }

    #[test]
    fn carries_the_last_id_forward_so_a_resume_knows_where_it_was() {
        // Promview sends the id once and later frames inherit it; losing it
        // would make the console resume from the wrong place after a drop.
        let mut parser = SseParser::new();
        parser.push("id: 7\nevent: a\ndata: 1\n\n");
        let events = parser.push("event: b\ndata: 2\n\n");
        assert_eq!(events[0].id, Some("7".to_string()));
    }

    #[test]
    fn joins_multiple_data_lines_the_way_the_spec_says() {
        let mut parser = SseParser::new();
        let events = parser.push("event: a\ndata: one\ndata: two\n\n");
        assert_eq!(events[0].data, "one\ntwo");
    }

    #[test]
    fn tolerates_crlf_and_a_field_with_no_value() {
        let mut parser = SseParser::new();
        let events = parser.push("event: a\r\ndata\r\n\r\n");
        assert_eq!(events.len(), 1);
        assert_eq!(events[0].data, "");
    }
}
