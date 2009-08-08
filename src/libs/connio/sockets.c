
#include <unistd.h>
#include <sys/poll.h>
#include <stdio.h>
#include <xpl.h>
#include <connio.h>
#include "config.h"

void
CHOP_NEWLINE(char *s)
{
	char *p; 
	p = strchr(s, 0x0A);
	if (p) {
		*p = '\0';
	} 
	p = strrchr(s, 0x0D);
	if (p) {
		*p = '\0';
	}
}

int
ConnTcpFlush(Connection *c, const char *b, const char *e, size_t *r)
{
	int Result=0;

	if (b < e) {
		char *curPTR = (char *)b;
		while (curPTR < e) {
			Result = ConnTcpWrite(c, curPTR, e - curPTR, r);
			if (*r > 0) {
				curPTR = curPTR + *r;
				continue;
			}
			break;
		}
		if (curPTR == e) {
			*r = e - b;
		} else {
			*r = 0;
            Result = -1;
		}
	} else {
		*r = 0;
	}

	return Result;
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

int
ConnTcpRead(Connection *c, char *b, size_t l, size_t *r) 
{
	int Result=0;

	struct pollfd pfd;
	do {
		pfd.fd = (int)c->socket;
		pfd.events = POLLIN;
		Result = poll(&pfd, 1, c->receive.timeOut * 1000);
		if (Result > 0) {
			if ((pfd.revents & (POLLIN | POLLPRI))) {
				do {
					if (!c->ssl.enable) {
						Result = IPrecv((c)->socket, b, l, 0);
					} else {
						Result = gnutls_record_recv(c->ssl.context, (void *)b, l);
					}
					if (Result >= 0) {
						CONN_TRACE_DATA(c, CONN_TRACE_EVENT_READ, b, Result);
						/* we actually worked.  reset Result to 0 indicating that */
						/* Result is > 0 so i should be able to safely cast it here */
						*r = (size_t)Result;
						Result = 0;
						break;
					} else if (errno == EINTR) {
						continue;
					}
					/* we failed with something other than eintr... we'll reset Result below */
					CONN_TRACE_ERROR(c, "RECV", Result);
					break;
				} while (TRUE);
				break;
			} else if ((pfd.revents & (POLLERR | POLLHUP | POLLNVAL))) {
				CONN_TRACE_ERROR(c, "POLL EVENT", Result);
				*r = 0;
				Result = -2;
				break;
			}
		}
		if (errno == EINTR) {
			continue;
		}
		CONN_TRACE_ERROR(c, "POLL", Result);
		Result = -3;
		*r = 0;
		break;
	} while (TRUE);

	return Result;
}

int
ConnTcpWrite(Connection *c, char *b, size_t l, size_t *r)
{
	int Result=0;

	do {
		if (!c->ssl.enable) {
			Result = IPsend(c->socket, b, l, MSG_NOSIGNAL);
		} else {
			Result = gnutls_record_send(c->ssl.context, (void *)b, l);
		}
		if (Result >= 0) {
			CONN_TRACE_DATA(c, CONN_TRACE_EVENT_WRITE, b, Result);
			/* we actually worked.  reset Result to 0 indicating that */
			/* Result is > 0 so i should be able to safely cast here */
			*r = Result;
			Result = 0;
			break;
		} else if (errno == EINTR) {
			continue;
		}
		CONN_TRACE_ERROR(c, "POLL", Result);
		Result = -1;
		*r = 0;
		break;
	} while (TRUE);

	return Result;
}

