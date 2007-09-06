// Xpl DNS resolution routines.

#include <xpl.h>
#include <sys/types.h>
#include <netinet/in.h>
#include <arpa/nameser.h>
#include <resolv.h>

#include "dns.h"
#include "config.h"

int
XplDnsInit()
{
	// res_init() may set errno, so we clear it and then test later
	errno = 0;
	(void) res_init();

	if (errno != 0)
		return -1;
	return 0;
}

int
_XplDns_ParseName(const char *response, const int response_len, const char *name, char *buffer, int buffer_len)
{
	const char *ptr;
	int len = 0;
	int result = 0;

	if (buffer_len < 2) return -1; // out of space
	len = buffer_len - strlen(buffer);
	if (len <= 0) return -1; // out of space

	ptr = name;
	while ((response <= ptr) && (ptr < (response + response_len))) {
		if (*ptr == '\0') {
			// we've reached the end
			ptr++;
			break;
		}
		if ((*ptr & 0xc0) == 0xc0) {
			int result;
			int newptr;
			// this is a pointer to a previous label
			newptr = (*ptr++ & 0x3f) * 256;
			newptr += *ptr++;
			if (newptr == (ptr - response - 2))
				return -4; // cyclical pointer; bad.
			result = _XplDns_ParseName(response, response_len, (response + newptr), buffer, buffer_len);
			if (result <= 0) return result;
			break;
		} else {
			// append the next label if we have room in the buffer
			if ((*ptr + 1) < len) {
				strncat(buffer, ptr + 1, *ptr);
				strncat(buffer, ".", 1);
				len -= *ptr + 1;
				ptr += *ptr + 1;
			} else {
				// out of room!
				return -1;
			}
		}
	}

	result = ptr - name;
	if ((result < 0) || (result > response_len)) 
		// a big mistake?
		return -2;

	return result;
}

#define IS_MX(r)	((r)->type == XPLDNS_RR_MX)

void
_XplDnsResult_AppendRecord(XplDns_Result *result, XplDns_RecordList *item)
{
	// this doesn't always append: if the item is an MX record, it will
	// be inserted before MX records of higher preference, or appended.
	// Thus, the record list always has MX records in order.
	// TOOD: Equal preference MX records are sorted in reply order (e.g., 
	// round-robin). We're supposed to randomise them.
	XplDns_RecordList *end;

	item->next = NULL;

	if (result->list == NULL) {
		// no items in list
		result->list = item;
	} else { //if (result->list->next == NULL) {
		// one item already in list
		end = result->list;
		// if this is an MX RR, see if we can insert before the first
		// item in our list
		if (IS_MX(item) && IS_MX(end)) {
			// TODO: if pref==pref, then we should randomise
			// See RFC 2821 #5
			if (end->record.MX.preference >= item->record.MX.preference) {
				item->next = result->list;
				result->list = item;
				return;
			}
		}
		
		while (end->next != NULL) {
			// see if we can insert before a later MX record
			if (IS_MX(item) && IS_MX(end->next)) {
				// See above TODO re: randomisation.
				if (end->next->record.MX.preference >= 
				    item->record.MX.preference) {
					// insert here!
					item->next = end->next;
					end->next = item;
					return;
				}
			}
			end = end->next;
		} 
		end->next = item;
	}
}

void
_XplDnsResult_PrintResults(XplDns_Result *result)
{
	XplDns_RecordList *item;

	if (result == NULL) return;
	item = result->list;

	printf ("\n== START OF DNS RECORD LIST\n");
	while (item != NULL) {
		struct sockaddr_in addr;

		switch (item->type) {
			case XPLDNS_RR_A:
				addr.sin_addr.s_addr = item->record.A.address;
				printf("%s IN A %s\n", item->record.A.name, inet_ntoa(addr.sin_addr));
				break;
			case XPLDNS_RR_CNAME:
				printf("%s IN CNAME %s\n", item->record.CNAME.name, item->record.CNAME.cname);
				break;
			case XPLDNS_RR_MX:
				printf("%s IN MX %d %s\n", 
					item->record.MX.name,
					item->record.MX.preference,
					item->record.MX.mxname);
				break;
			default:
				printf("Unknown record.\n");
				break;
		}
		item = item->next;
	}
	printf ("== END OF RESULTS\n");
}

void
XplDnsResultFree(XplDns_Result *result)
{
	XplDns_RecordList *item = result->list;
	XplDns_RecordList *last;

	if (result == NULL) return;

	while (item != NULL) {
		last = item;
		item = item->next;
		MemFree(last);
	}
	MemFree(result);
}

XplDns_MxLookup *
XplDnsNewMxLookup(const char *domain)
{
	// Lazy MX resolver.
	// Should comply with RFC 2821 #5
	XplDns_MxLookup *result;
	XplDns_Result *mx_query;
	char mail_domain[XPLDNS_NAMELEN + 1];
	int cnames_found = 0;

	result = MemMalloc0(sizeof(XplDns_MxLookup));
	if (! result) return NULL;

	strncpy(mail_domain, domain, XPLDNS_NAMELEN);

	// we come back to this point if we encounter CNAMEs before MX RRs
restart_query:
	mx_query = _XplDns_Query(mail_domain, XPLDNS_RR_MX);

	if (mx_query->status == XPLDNS_NO_DATA) {
		// domain exists, but no MX records
		XplDns_Result *a_query;

		a_query = _XplDns_Query(domain, XPLDNS_RR_A);
		if (a_query->status == XPLDNS_SUCCESS) {
			// A RR exists, so let's fake an MX record for it
			XplDnsResultFree(mx_query);
			XplDnsResultFree(a_query);
			mx_query = _XplDns_QueryFakeMx(domain);
		}	
	} else if (mx_query->status == XPLDNS_SUCCESS) {
		// check we don't have any CNAME in results
		XplDns_RecordList *record = mx_query->list;
		
		while (record) {
			if (record->type == XPLDNS_RR_CNAME) {
				// restart our query on new domain
				strncpy(mail_domain, record->record.CNAME.cname,
					XPLDNS_NAMELEN);
				XplDnsResultFree(mx_query);

				cnames_found++;
				if (cnames_found < 6) {
					goto restart_query;
				} else {
					result->status = XPLDNS_FAIL_CNAME_LOOP;
					return result;	
				}
			} else {
				record = record->next;
			}
		}
	}

	result->status = mx_query->status;
	result->_mx_result = mx_query;
	result->_mx_current = mx_query->list;

	return result;
}

XplDns_IpList *
XplDnsNextMxLookupIpList(XplDns_MxLookup *mx)
{
	XplDns_IpList *result;
	XplDns_Result *lookup;
	XplDns_RecordList *record;
	const char *mail_exchanger = NULL;
	
	if (mx->_mx_current == NULL) return NULL;
	result = MemMalloc(sizeof(XplDns_IpList));
	if (! result) return NULL;

	result->status = XPLDNS_TRY_AGAIN;
	result->number = 0;

	if (mx->_mx_current->type == XPLDNS_RR_MX) 
		// want to make sure we're looking at the right records.
		mail_exchanger = mx->_mx_current->record.MX.mxname;

	// no decent records at this point?
	if (! mail_exchanger) return result;
	
	strncpy(mx->current_mx, mail_exchanger, XPLDNS_NAMELEN);
	mx->preference = mx->_mx_current->record.MX.preference;
	mx->_mx_current = mx->_mx_current->next;

	// look up the mail server's IP address(es)
	lookup = _XplDns_Query(mail_exchanger, XPLDNS_RR_A);

	if (lookup->status != XPLDNS_SUCCESS) {
		// more failure... :/
		result->status = lookup->status;
		return result;
	}

	record = lookup->list;
	while (record) {
		// FIXME: check for CNAME entries?
		if (record->type != XPLDNS_RR_A) {
			record = record->next;
			continue; // not interested in non-A records
		}
		
		result->ip_list[result->number] = record->record.A.address;
		result->number++;
		if (result->number >= XPLDNS_MAX_IP_IN_LIST)
			break;
		record = record->next;
	}
	
	XplDnsResultFree(lookup);
	return result;
}

void
XplDnsFreeMxLookup(XplDns_MxLookup *mx)
{
	if (mx == NULL) return;

	if (mx->_mx_result) {
		XplDnsResultFree(mx->_mx_result);
		mx->_mx_result = NULL;
	}

	MemFree(mx);
}

XplDns_Result *
_XplDns_QueryFakeMx(const char *domain)
{
	// when a domain has no MX RR, RFC 2821 says treat any A record
	// for that domain as having an implicit MX record. This creates
	// that record, as if it existed in DNS.
	XplDns_Result *result;
	XplDns_RecordList *list;
	
	list = MemMalloc(sizeof(XplDns_RecordList));

	strncpy(list->record.MX.name, domain, XPLDNS_NAMELEN);
	strncpy(list->record.MX.mxname, domain, XPLDNS_NAMELEN);
	list->record.MX.preference = 0;
	list->type = XPLDNS_RR_MX;
	
	result = MemMalloc(sizeof(XplDns_Result));
	result->list = list;
	result->status = XPLDNS_SUCCESS;

	return result;
}

XplDns_Result *
_XplDns_Query(const char *domain, XplDns_RecordType type)
{
	XplDns_Result *result;
	char answer_buffer[4096];
	int answer_len = -1;

	result = MemMalloc(sizeof(XplDns_Result));
	result->list = NULL;
	result->status = XPLDNS_FAIL;

	// FIXME: is this re-entrant _everywhere_ ?
	answer_len = res_query(domain, ns_c_any, type, answer_buffer, 4096);

	if (answer_len < 0) {
		// didn't get a sensible answer
		if (h_errno) { 
			result->status = (XplDns_ResultCode) h_errno;
			return result;
		} else {
			return result;
		}
	}

	if (answer_len == 0) {
		// no records in the answer
		result->status = XPLDNS_NO_DATA;
		return result;
	}

	_XplDns_ParseQuery(answer_buffer, answer_len, result);

	return result;
}

void
_XplDns_ParseQuery(const char *answer_buffer, int answer_len, XplDns_Result *result)
{
	XplDnsResponseHeader *header;
	const char *response, *response_end;
	int x;
	int res;
	int answers;

	// try to parse the answer
	header = (XplDnsResponseHeader *)answer_buffer;
	response = answer_buffer + sizeof(XplDnsResponseHeader);
	response_end = answer_buffer + answer_len;

	for (x = 0; x < ntohs(header->qdcount); x++) {
		int size;
		char buffer[1024];

		buffer[0] = '\0';
		size = _XplDns_ParseName(answer_buffer, answer_len, response, buffer, 1000);
		// did we find the name correctly?
		if (size < 0) return;

		// Skip QCODE / QCLASS
		response += size + 4;

		// does the buffer extent look right?
		if (response >= response_end) return;
	}

	for (x = 0; x < ntohs(header->ancount); x++) {
		XplDnsAnswer *dns_answer;
		XplDns_RecordList *item;
		char answer_domain[XPLDNS_NAMELEN + 1];

		item = MemMalloc(sizeof(XplDns_RecordList));

		answer_domain[0] = '\0';
		res = _XplDns_ParseName(answer_buffer, answer_len, 
			response, answer_domain, XPLDNS_NAMELEN);
		if (res < 0) return; // couldn't parse name...

		response += res;
		if (response >= response_end) return;

		dns_answer = (XplDnsAnswer *)response;

		response += sizeof(XplDnsAnswer);
		if (response >= response_end) return;

		// TODO : check ntohs(ans->class) is right

		switch(ntohs(dns_answer->rtype)) {
			case XPLDNS_RR_A: {
				item->type = XPLDNS_RR_A;
				XplDnsAnswerARecord *rec = (XplDnsAnswerARecord *)response;
				strncpy(item->record.A.name, answer_domain, XPLDNS_NAMELEN);
				item->record.A.address = rec->address;
				_XplDnsResult_AppendRecord(result, item);
				}
				break;
			case XPLDNS_RR_CNAME: {
				item->type = XPLDNS_RR_CNAME;

				strncpy(item->record.CNAME.name, answer_domain,
					XPLDNS_NAMELEN);

				item->record.CNAME.cname[0] = '\0';
				_XplDns_ParseName(answer_buffer, answer_len, response, item->record.CNAME.cname, XPLDNS_NAMELEN);
				_XplDnsResult_AppendRecord(result, item);

				}
				break;
			case XPLDNS_RR_MX: {
				XplDnsAnswerMxRecord *rec = (XplDnsAnswerMxRecord *)response;
				int r;

				strncpy(item->record.MX.name, answer_domain,
					XPLDNS_NAMELEN);
				item->type = XPLDNS_RR_MX;
				item->record.MX.preference = ntohs(rec->pref);
				
				item->record.MX.mxname[0] = '\0';
				r = _XplDns_ParseName(answer_buffer,
					answer_len, 
					response + sizeof(XplDnsAnswerMxRecord),
					item->record.MX.mxname, 
					XPLDNS_NAMELEN);
				if (r >= 0) {
					_XplDnsResult_AppendRecord(result, item);
				} else {
					return;
				}
				}
				break;
			default:
				// unknown type.
				MemFree(item);
				break;
		}

		response += ntohs(dns_answer->size);
		if (response > response_end) return;
	}

	// don't make the result 'success' until we've really done everything
	result->status = XPLDNS_SUCCESS;
	return;
}
