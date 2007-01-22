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
QP_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;


	while (Len<Codec->Len) {
		FlushOutStream(6);

		if (In[0]=='=') {
			if ((Codec->Len-Len)>=3) {
				In++;
				Len++;
				if ((In[0]=='\r') || (In[0]=='\n')) {
					/* According to spec, a '=\r\n' sequence is a soft newline and get's eaten; we also try to handle '=\n' only */
					In++;
					Len++;
					if (In[0]=='\n') {
						In++;
						Len++;
					}
				} else {
					/* It's a hex char to be decoded */
					In[0] |= 0x20;
					if (In[0] > 64) {
						Out[0]=(In[0]-'a'+10)<<4;
					} else {
						Out[0]=(In[0]-'0')<<4;
					}
					In++;
					Len++;

					In[0] |= 0x20;
					if (In[0] > 64) {
						Out[0] |= (In[0]-'a'+10);
					} else {
						Out[0] |= (In[0]-'0');
					}
					Out++;
					NextCodec->Len++;
					In++;
					Len++;
				}
			} else {
				if (Codec->EOS) {
					/* No more data, let's fake it */
					Out[0]=In[0];
					Out++;
					NextCodec->Len++;
					In++;
					Len++;
				} else {
					/* Need more data? */
				    if (Len >= Codec->Min) {
					return(Len);
				    }

				    return(Codec->Len);
				}
			}
		} else {
			Out[0]=In[0];
			Out++;
			NextCodec->Len++;
			In++;
			Len++;
		}
	}

	EndFlushStream;

	return(Len);
}

static	unsigned char *HEX = {"0123456789ABCDEF"};
#define	MAX_CHARS_PER_LINE	75

static int
QP_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;


	while (Len<Codec->Len) {
		FlushOutStream(6);

		if ((Len+1==Codec->Len) && !Codec->EOS) {
		    if (Len >= Codec->Min) {
			return(Len);
		    }

		    return(Codec->Len);
		}

		if (In[0]=='\r' || In[0]=='\n') {
			Codec->State=0;		/* Char/per/Line/count */
			Out[0]=In[0];
			Out++;
			NextCodec->Len++;
			In++;
			Len++;
			if (Len<Codec->Len) {
				if (In[0]=='\n') {
					Out[0]=In[0];
					Out++;
					NextCodec->Len++;
					In++;
					Len++;
				} else {
					/* Broken line; there was a \n but on \r first; we're nice and fix it */
					Out[-1]='\r';
					Out[0]='\n';
					Out++;
					NextCodec->Len++;
				}
			}
		} else if ((In[0]<0x20) || (In[0]>0x7f) || (In[0]=='=') || (!Codec->EOS && ((In[0]==' ') && (In[1]=='\r')))) {		/* The last expression will find <SPACE><CR> and will make create linebreaks */
			if ((Codec->State+=3)>MAX_CHARS_PER_LINE) {			/* Watch it, we're incrementing Codec->State here!  */
				Out[0]='=';
				Out[1]='\r';
				Out[2]='\n';
				Out+=3;
				NextCodec->Len+=3;
				Codec->State=3;
			}
			Out[0]='=';
			Out[1]=HEX[In[0] >> 4];
			Out[2]=HEX[In[0] & 0x0f];
			In++;
			Len++;
			Out+=3;
			NextCodec->Len+=3;
		} else {
			if ((++Codec->State)>MAX_CHARS_PER_LINE) {			/* Watch it, we're incrementing Codec->State here! */
				Out[0]='=';
				Out[1]='\r';
				Out[2]='\n';
				Out+=3;
				NextCodec->Len+=3;
				Codec->State=3;
			}
			Out[0]=In[0];
			In++;
			Len++;
			Out++;
			NextCodec->Len++;
		}
	}

	EndFlushStream;

	return(Len);
}


