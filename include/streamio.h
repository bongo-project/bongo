/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2001 Novell, Inc. All Rights Reserved.
 * 
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of version 2 of the GNU General Public License
 * as published by the Free Software Foundation.
 * 
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 * 
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, contact Novell, Inc.
 * 
 * To contact Novell about this file by physical or electronic mail, you 
 * may find current contact information at www.novell.com.
 * </Novell-copyright>
 ****************************************************************************/

#ifndef _STREAMIO_H_
#define _STREAMIO_H_

#include <xpl.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct {
	void				*Client;
	void				*Session;
	void				*ModuleSession;
	unsigned char	*MIMEType;
	void				*StreamData;
} MIMEDisplayStruct;

typedef struct _StreamStruct {
    unsigned char *Start;                                                           /* Start of the buffer */
    unsigned long Len;                                                              /* Indicates how much data is in the buffer */
    unsigned char *End;                                                             /* Points to the physical end of the buffer */
    unsigned long State;                                                            /* Codecs can keep state with this */
    BOOL EOS;                                                                       /* End of Stream indicator */
    unsigned long Min;                                                              /* A codec must alway process more bytes than Min (defaults to 0) */
    void *Charset;                                                                  /* MIME filters; contains the users charset */
    void *URL;                                                                      /* MIME filters; contains the redirect URL */
    void *StreamData;                                                               /* For private use of the stream module */
    void *StreamData2;                                                              /* For private use of the stream module */
    unsigned long StreamLength;                                                     /* For private use of the stream module */
    void *Client;                                                                   /* Client structure */
    int (*Codec)(struct _StreamStruct *Stream, struct _StreamStruct *NextStream);   /* Function processing *this* buffer */
    struct _StreamStruct *Next;                                                     /* Next stream element or NULL */
} StreamStruct;

#define CHAR_SET_TYPE_NONE		0
#define CHAR_SET_TYPE_8BIT		1
#define CHAR_SET_TYPE_16BIT	2
#define CHAR_SET_TYPE_VBIT		3
#define CHAR_SET_TYPE_2022		4
#define CHAR_SET_TYPE_UTF8		5


typedef struct {
	unsigned char		*Charset;
	int 					(*Decoder)(struct _StreamStruct *Stream, struct _StreamStruct *NextStream);
	int 					(*Encoder)(struct _StreamStruct *Stream, struct _StreamStruct *NextStream);
	unsigned long		CharsetType;
	BOOL					(*TemplateHandler)(void *Client, void *Session, unsigned long TemplateID, void *Token, void *GotoToken, void *ObjectData);
} StreamDescStruct;

typedef int				(*StreamCodecFunc)(struct _StreamStruct *Stream, struct _StreamStruct *NextStream);
typedef BOOL			(*TemplateHandlerFunc)(void *Client, void *Session, unsigned long TemplateID, void *Token, void *GotoToken, void *ObjectData);

/* Helpers from streamio.c */
StreamCodecFunc	FindCodec(unsigned char *Charset, BOOL Encoder);
StreamCodecFunc	FindCodecDecoder(unsigned char *Charset);
StreamCodecFunc	FindCodecEncoder(unsigned char *Charset);
BOOL StreamioInit(void);
BOOL StreamioShutdown(void);

extern StreamDescStruct StreamList[];

#ifdef __cplusplus
}
#endif

#endif  /* _STREAMIO_H_ */

