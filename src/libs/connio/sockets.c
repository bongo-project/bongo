
#include <unistd.h>
#include <sys/poll.h>
#include <stdio.h>
#include <xpl.h>
#include <connio.h>
#include "config.h"

void
CHOP_NEWLINE(unsigned char *s)
{
	unsigned char *p; 
	p = strchr(s, 0x0A);
	if (p) {
		*p = '\0';
	} 
	p = strrchr(s, 0x0D);
	if (p) {
		*p = '\0';
	}
}

void
SET_POINTER_TO_VALUE(unsigned char *p, unsigned char *s)
{
	// FIXME: Unused?
	p = s;
	while(isspace(*p)) {
		p++;
	}
	if ((*p == '=') || (*p == ':')) {
		p++;
	}
	while(isspace(*p)) {
		p++;
	}
}

void
ConnTcpFlush(Connection *c, const char *b, const char *e, int *r)
{
	if (b < e) {
		char *curPTR = (char *)b;
		while (curPTR < e) {
			ConnTcpWrite(c, curPTR, e - curPTR, r);
			if (*r > 0) {
				curPTR = curPTR + (int)*r;
				continue;
			}
			break;
		}
		if (curPTR == e) {
			*r = e - b;
		} else {
			*r = -1;
		}
	} else {
		*r = 0;
	}
}

void
ConnTcpClose(Connection *c)
{
	if (c->receive.buffer) {
		c->receive.remaining = CONN_TCP_MTU;
	} else {
		c->receive.remaining = 0;
	}
	c->receive.read = c->receive.write = c->receive.buffer;
	if (c->send.buffer) {
		ConnFlush(c);
		c->send.remaining = CONN_TCP_MTU;
	} else {
		c->send.remaining = 0;
	}
	c->send.read = c->send.write = c->send.buffer;
	if (c->ssl.enable) {
		gnutls_bye(c->ssl.context, GNUTLS_SHUT_RDWR);
		gnutls_deinit(c->ssl.context);
		c->ssl.context = NULL;
		c->ssl.enable = FALSE;
	}
	if (c->socket != -1) {
		IPshutdown(c->socket, 2);
		IPclose(c->socket);
		c->socket = -1;
	}
}

//#if defined(S390RH) || defined(SOLARIS)

// TODO: is this correct for Solaris?

void
ConnTcpRead(Connection *c, char *b, size_t l, int *r) 
{
	struct pollfd pfd;
	do {
		pfd.fd = (int)c->socket;
		pfd.events = POLLIN;
		*r = poll(&pfd, 1, c->receive.timeOut * 1000);
		if (*r > 0) {
			if ((pfd.revents & (POLLIN | POLLPRI))) {
				do {
					if (!c->ssl.enable) {
						*r = IPrecv((c)->socket, b, l, 0);
					} else {
						*r = gnutls_record_recv(c->ssl.context, (void *)b, l);
					}
					if (*r >= 0) {
						CONN_TRACE_DATA(c, CONN_TRACE_EVENT_READ, b, *r);
						break;
					} else if (errno == EINTR) {
						continue;
					}
					CONN_TRACE_ERROR(c, "RECV", *r);
					break;
				} while (TRUE);
				break;
			} else if ((pfd.revents & (POLLERR | POLLHUP | POLLNVAL))) {
				CONN_TRACE_ERROR(c, "POLL EVENT", *r);
				*r = -2;
				break;
			}
		}
		if (errno == EINTR) {
			continue;
		}
		CONN_TRACE_ERROR(c, "POLL", *r);
		*r = -3;
		break;
	} while (TRUE);
}

void
ConnTcpWrite(Connection *c, char *b, size_t l, int *r)
{
	do {
		if (!c->ssl.enable) {
			*r = IPsend(c->socket, b, l, MSG_NOSIGNAL);
		} else {
			*r = gnutls_record_send(c->ssl.context, (void *)b, l);
		}
		if (*r >= 0) {
			CONN_TRACE_DATA(c, CONN_TRACE_EVENT_WRITE, b, *r);
			break;
		} else if (errno == EINTR) {
			continue;
		}
		CONN_TRACE_ERROR(c, "POLL", *r);
		*r = -1;
		break;
	} while (TRUE);
}

//#else
#if 0

void
ConnTcpRead(Connection *c, char *b, size_t l, int *r)
{
	fd_set rfds;
	struct timeval timeout;
	FD_ZERO(&rfds);
	FD_SET(c->socket, &rfds);
	timeout.tv_usec = 0;
	timeout.tv_sec = c->receive.timeOut;
	*r = select(FD_SETSIZE, &rfds, NULL, NULL, &timeout);
	if (*r > 0) {
		*r = recv(c->socket, b, l, 0);
		CONN_TRACE_DATA_AND_ERROR(c, CONN_TRACE_EVENT_READ, b, *r, "RECV");
	} else {
		CONN_TRACE_ERROR(c, "SELECT", *r);
		*r = -1;
	}
}

void
ConnTcpWrite(Connection *c, char *b, size_t l, int *r)
{
	*r = IPsend(c->socket, b, l, 0);
	CONN_TRACE_DATA_AND_ERROR(c, CONN_TRACE_EVENT_WRITE, b, *r, "SEND");
}


#endif
