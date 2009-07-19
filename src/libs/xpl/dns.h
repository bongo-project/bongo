
// private DNS library structures

#ifndef XPLDNS_P_H
#define XPLDNS_P_H

#include <xpldns.h>

#if defined(WIN32) || defined(NETWARE) || defined(LIBC)
#pragma pack (push, 1)
#endif

// these structs represent DNS protocol data structures

typedef struct {
	unsigned  id :16;	// query ID number
#if __BYTE_ORDER == __BIG_ENDIAN
	// byte three
	unsigned  qr: 1;	// is this a response?
	unsigned  opcode: 4;	// type of query
	unsigned  aa: 1;	// is answer authoritative?
	unsigned  tc: 1;	// is answer truncated?
	unsigned  rd: 1;	// do we want to recurse?
	// byte four
	unsigned  ra: 1;	// is recursion available?
	unsigned  unused :1;	// not used
	unsigned  ad: 1;	// "authentic" data
	unsigned  cd: 1;	// resolver disabled checking
	unsigned  rcode :4;	// response code
#endif
#if __BYTE_ORDER == __LITTLE_ENDIAN || __BYTE_ORDER == __PDP_ENDIAN
	// same structure as above, just in a different order
	// byte three
	unsigned  rd :1;
	unsigned  tc :1;
	unsigned  aa :1;
	unsigned  opcode :4;
	unsigned  qr :1;
	// byte four
	unsigned  rcode :4;
	unsigned  cd: 1;
	unsigned  ad: 1;
	unsigned  unused :1;
	unsigned  ra :1;
#endif
	unsigned  qdcount :16;	// no. question entries
	unsigned  ancount :16;	// no. answer entries
	unsigned  nscount :16;	// no. authority/name server entries
	unsigned  arcount :16;	// no. resource entries
} XplDnsResponseHeader;

typedef struct  {
	unsigned short rtype;	// resource type
	unsigned short class;	// protocol class
	unsigned int ttl;	// time to live
	unsigned short size;	// answer size
} Xpl8BitPackedStructure XplDnsAnswer;

typedef struct {
	unsigned int address;	// the IP address
} Xpl8BitPackedStructure XplDnsAnswerARecord;

typedef struct {
	unsigned short pref;	// MX record preference
} Xpl8BitPackedStructure XplDnsAnswerMxRecord;

#if defined(WIN32) || defined(NETWARE) || defined(LIBC)
#pragma pack (pop)
#endif

int 	_XplDns_ParseName(const unsigned char *response, const int response_len, const unsigned char *name, char *buffer, int buffer_len);
void 	_XplDns_ParseQuery(const unsigned char *answer_buffer, int answer_len, XplDns_Result *result);
void 	_XplDnsResult_AppendRecord(XplDns_Result *result, XplDns_RecordList *item);
XplDns_Result * _XplDns_QueryFakeMx(const char *domain);
void	 _XplDnsResult_PrintResults(XplDns_Result *result);
XplDns_Result *  _XplDns_Query(const char *domain, XplDns_RecordType type);

#endif
