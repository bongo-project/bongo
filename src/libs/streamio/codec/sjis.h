/*
 * Copyright (C) 1999-2002 Free Software Foundation, Inc.
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
SJIS_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;

	while (Len<Codec->Len) {
		FlushOutStream(6);

		if ((In[0]<0x80) || (In[0]>=0xa1 && In[0]<=0xdf)) {
			/* JIS x 0201 */
			if (In[0]==0x5c) {
				*Out=0xa5;
				Out++;
				In++;
				NextCodec->Len++;
				Len++;
			} else if (In[0]==0x7e) {
				UTF8EncodeChar(0x203e, Out, Out, NextCodec->Len);
				In++;
				Len++;
			} else {
				if (In[0]<0x80) {
					*Out=*In;
					Out++;
					In++;
					NextCodec->Len++;
					Len++;
				} else {
				    register unsigned short wc = (unsigned short)In[0];

					UTF8EncodeChar(wc, Out, Out, NextCodec->Len);
					In++;
					Len++;
				}
			}
		} else {
			CheckInStream(2);
			if ((In[0] > 0x81 && In[0]<=0x9f) || (In[0]>=0xe0 && In[0]<=0xea)) {
				if ((In[1] >= 0x40 && In[1] <= 0x7e) || (In[1] >= 0x80 && In[1] <= 0xfc)) {
					unsigned int	Index;
					unsigned short	wc = 0xfffd;
					unsigned char	T1=(In[0]<0xe0 ? In[0]-0x81 : In[0]-0xc1);
					unsigned char	T2=(In[1]<0x80 ? In[1]-0x40 : In[1]-0x41);
					unsigned char	C1;
					unsigned char	C2;

					C1=2*T1+(T2<0x5e ? 0 : 1);
					C2=(T2<0x5e ? T2 : T2-0x5e);

					Index=94 * (C1) + (C2);

					if (Index<1410) {
						if (Index<690) {
							wc=jisx0208_2uni_page21[Index];
						}
					} else {
						if (Index<7808) {
							wc=jisx0208_2uni_page30[Index-1410];
						}
					}
					if (wc != 0xfffd) {
						UTF8EncodeChar(wc, Out, Out, NextCodec->Len);
					}
				}
			} else if (In[0]>=0xf0 && In[0]<=0xf9) {
				/* User-defined; from Ken Lunde "CJKV Information Processing" */
				if ((In[1]>0x40 && In[1]<=0x7e) || (In[1]>=0x80 && In[1]<=0xfc)) {
					UTF8EncodeChar(0xe000+188*(In[0]-0xf0)+(In[1]<0x80 ? In[1]-0x40 : In[1]-0x41), Out, Out, NextCodec->Len);
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
SJIS_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;
	unsigned long	wc;

	while (Len<Codec->Len) {
		FlushOutStream(6);

		if (*In<0x80) {
			Out[0]=In[0];
			Out++;
			In++;
			NextCodec->Len++;
			Len++;
		} else {
			const Summary16 *summary = NULL;

			CheckUTFInStream;

			/* Decode the UTF char */
			UTF8DecodeChar(In, wc, Len, Codec->Len);

			/* This checks JIS 0201 */
			if (wc<0x0080 || (wc>=0xff61 && wc<0xffa0)) {
				if (wc==0x00a5) {
					Out[0]=0x5c;
					Out++;
					NextCodec->Len++;
				} else if (wc==0x203e) {
					Out[0]=0x7e;
					Out++;
					NextCodec->Len++;
				} else if (wc>=0xff61) {
					Out[0]=(unsigned char)(wc-0xfec0);
					Out++;
					NextCodec->Len++;
				} else {
					Out[0]=In[0];
					Out++;
					NextCodec->Len++;
				}
			} else {
				/* Wasn't JIS 0201, try 0208 */

				if (wc >= 0x0000 && wc < 0x0100) {
					summary = &jisx0208_uni2indx_page00[(wc>>4)];
				} else if (wc >= 0x0300 && wc < 0x0460) {
					summary = &jisx0208_uni2indx_page03[(wc>>4)-0x030];
				} else if (wc >= 0x2000 && wc < 0x2320) {
					summary = &jisx0208_uni2indx_page20[(wc>>4)-0x200];
				} else if (wc >= 0x2500 && wc < 0x2670) {
					summary = &jisx0208_uni2indx_page25[(wc>>4)-0x250];
				} else if (wc >= 0x3000 && wc < 0x3100) {
					summary = &jisx0208_uni2indx_page30[(wc>>4)-0x300];
				} else if (wc >= 0x4e00 && wc < 0x9fb0) {
					summary = &jisx0208_uni2indx_page4e[(wc>>4)-0x4e0];
				} else if (wc >= 0xff00 && wc < 0xfff0) {
					summary = &jisx0208_uni2indx_pageff[(wc>>4)-0xff0];
				}
				if (summary) {
					unsigned short	used	= summary->used;
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
						c = jisx0208_2charset[summary->indx + used];

						if ((c>>8<0x80) && ((c & 0xff) < 0x80)) {
							unsigned char	T1=((c>>8)-0x21)>>1;
							unsigned char	T2=((((c>>8)-0x21) & 1) ? 0x5e : 0) + ((c&0xff)-0x21);

							Out[0]=(T1 < 0x1f ? T1+0x81 : T1+0xc1);
							Out[1]=(T2 < 0x3f ? T2+0x40 : T2+0x41);
							Out+=2;
							NextCodec->Len+=2;
							continue;
						}
					}
				}
				/* We get here if we didn't match 0201 and 0208; must be user defined */
				if (wc>=0xe000 && wc<0xe758) {
					unsigned char	C;

					C=(unsigned int)(wc-0xe000) % 188;
					Out[0]=((unsigned int)(wc-0xe000) / 188) + 0xf0;
					Out[1]=(C < 0x3f ? C+0x40 : C+0x41);
					Out+=2;
					NextCodec->Len+=2;
				}
			}
		}
	}

	EndFlushStream;

	return(Len);
}
