/*
 * Copyright (C) 1999-2001 Free Software Foundation, Inc.
 * This file is part of the GNU LIBICONV Library.
 *
 * The GNU LIBICONV Library is free software; you can redistribute it
 * and/or modify it under the terms of the GNU Library General Public
 * License as published by the Free Software Foundation; either version 2
 * of the License, or (at your option) any later version.
 *
 * The GNU LIBICONV Library is distributed in the hope that it will be
 * useful, but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU
 * Library General Public License for more details.
 *
 * You should have received a copy of the GNU Library General Public
 * License along with the GNU LIBICONV Library; see the file COPYING.LIB.
 * If not, write to the Free Software Foundation, Inc., 59 Temple Place -
 * Suite 330, Boston, MA 02111-1307, USA.
 */

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

static int
HZ_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;

	while (Len<Codec->Len) {
		FlushOutStream(6);

		if (In[0]=='~') {
			CheckInStream(3);

			if (Codec->State==0) {
				if (In[1]=='~') {
					Out[0]='~';
					Out++;
					NextCodec->Len++;
					In+=2;
					Len+=2;
				} else if (In[1]=='{') {
					Codec->State=1;
					In+=2;
					Len+=2;
					CheckInStream(2);
				} else if (In[0]=='\n') {
					Out[0]=In[0];
					In+=2;
					Len+=2;
					Out++;
					NextCodec->Len++;
				}
			} else {
				if (In[1]=='}') {
					Codec->State=0;
					In+=2;
					Len+=2;
				}
			}
		}
	
		if (Codec->State==0) {
			Out[0]=In[0];
			Out++;
			In++;
			NextCodec->Len++;
			Len++;
		} else {
			CheckInStream(2);

			if ((In[0] >= 0x21 && In[0] <= 0x29) || (In[0] >= 0x30 && In[0] <= 0x77)) {
				if (In[1] >= 0x21 && In[1]<0x7f) {
					unsigned int	i	= 94 * (In[0] - 0x21) + (In[1] - 0x21);
					unsigned short	wc = 0xfffd;

					if (i < 1410) {
						if (i < 831) {
							wc = gb2312_2uni_page21[i];
						}
					} else {
						if (i < 8178) {
							wc = gb2312_2uni_page30[i-1410];
						}
					}
					if (wc != 0xfffd) {
						UTF8EncodeChar(wc, Out, Out, NextCodec->Len);
					}
				}
			}
			In+=2;
			Len+=2;
		}
	}

	EndFlushStream;

	return(Len);
}


static int
HZ_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;
	unsigned long	wc;

	while (Len<Codec->Len) {
		FlushOutStream(6);

		if (*In<0x80) {
			if (Codec->State!=0) {
				/* Reset to ascii stream */
				Out[0]='~';
				Out[1]='}';
				Out+=2;
				NextCodec->Len+=2;
				Codec->State=0;
			}
			Out[0]=In[0];
			Out++;
			In++;
			NextCodec->Len++;
			Len++;
		} else {
			const Summary16 *summary = NULL;

			CheckUTFInStream;

			UTF8DecodeChar(In, wc, Len, Codec->Len);

			if (wc >= 0x0000 && wc < 0x0460) {
				summary = &gb2312_uni2indx_page00[(wc>>4)];
			} else if (wc >= 0x2000 && wc < 0x2650) {
				summary = &gb2312_uni2indx_page20[(wc>>4)-0x200];
			} else if (wc >= 0x3000 && wc < 0x3230) {
				summary = &gb2312_uni2indx_page30[(wc>>4)-0x300];
			} else if (wc >= 0x4e00 && wc < 0x9cf0) {
				summary = &gb2312_uni2indx_page4e[(wc>>4)-0x4e0];
			} else if (wc >= 0x9e00 && wc < 0x9fb0) {
				summary = &gb2312_uni2indx_page9e[(wc>>4)-0x9e0];
			} else if (wc >= 0xff00 && wc < 0xfff0) {
				summary = &gb2312_uni2indx_pageff[(wc>>4)-0xff0];
			}
			if (summary) {
				unsigned short used	= summary->used;
				unsigned int	i		= wc & 0x0f;

				if (used & ((unsigned short) 1 << i)) {
					unsigned short c;
					/* Keep in `used' only the bits 0..i-1. */
					used &= ((unsigned short) 1 << i) - 1;
					/* Add `summary->indx' and the number of bits set in `used'. */
					used = (used & 0x5555) + ((used & 0xaaaa) >> 1);
					used = (used & 0x3333) + ((used & 0xcccc) >> 2);
					used = (used & 0x0f0f) + ((used & 0xf0f0) >> 4);
					used = (used & 0x00ff) + (used >> 8);
					c = gb2312_2charset[summary->indx + used];

					/* Switch to HZ */
					if (Codec->State==0) {
						Out[0]='~';
						Out[1]='{';
						Out+=2;
						NextCodec->Len+=2;
						Codec->State=1;
					}
					Out[0] = (c >> 8); 
					Out[1] = (c & 0xff);
					Out+=2;
					NextCodec->Len+=2;
				}
			}
		}
	}

	EndFlushStream;

	return(Len);
}
