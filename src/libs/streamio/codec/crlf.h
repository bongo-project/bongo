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
CRLF_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;

	while (Len<Codec->Len) {
		FlushOutStream(2);

		switch(In[0]) {
			case '\r': {
				if (Len+2>Codec->Len) {
					if (!Codec->EOS) {
					    if (Len >= Codec->Min) {
						return(Len);
					    }

					    return(Codec->Len);
					} else {
						Out[0]='\r';
						Out[1]='\n';
						Out+=2;
						In++;
						NextCodec->Len+=2;
						Len++;
						continue;
					}
				}
				if (In[1]=='\n') {
					Out[0]='\r';
					Out[1]='\n';
					Out+=2;
					In+=2;
					NextCodec->Len+=2;
					Len+=2;
					continue;
				} else {
					Out[0]='\r';
					Out[1]='\n';
					Out+=2;
					In++;
					NextCodec->Len+=2;
					Len++;
					continue;
				}
			}

			case '\n': {
				Out[0]='\r';
				Out[1]='\n';
				Out+=2;
				In++;
				NextCodec->Len+=2;
				Len++;
				continue;
			}

			default: {
				Out[0]=In[0];
				Out++;
				In++;
				NextCodec->Len++;
				Len++;
				continue;
			}
		}
	}

	EndFlushStream;

	return(Len);
}

