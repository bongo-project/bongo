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

#if VCARD_CODEC_COMPLETE

static int
VCard_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;
	int				i;

	if (Codec->StreamData==NULL && Codec->Len>0) {
		FlushOutStream(25);
		i = snprintf(Out, OutEnd - Out, "<TABLE BORDER=2><TR><TD>");
		Out+=i;
		NextCodec->Len+=i;
		Codec->StreamData=(void *)1;
	}

	while (Len<Codec->Len) {
		FlushOutStream(20);
		if (Codec->State==0) {
			if (In[0]==':') {
				Codec->State=1;
				In++;
				Len++;
				continue;
			}
		} else {
			if (In[0]==0x0a) {
				Codec->State=0;
				In++;
				Len++;
				i = snprintf(Out, OutEnd - Out, "</TD><TD>");
				Out+=i;
				NextCodec->Len+=i;
				continue;
			}
		}

		if (Codec->State==1) {
			Out[0]=In[0];
			Out++;
			In++;
			Len++;
			NextCodec->Len++;
			continue;
		} else {
			/* We're skipping the chars */
			In++;
			Len++;
			continue;
		}
	}

	if ((NextCodec->Len>0) || (Codec->EOS)) {
		if ((Len==Codec->Len) && (Codec->EOS)) {
			NextCodec->EOS=TRUE;
		}
		if (NextCodec->EOS && Codec->StreamData!=NULL) {
			i = snprintf(Out, OutEnd - Out, "</TD></TR></TABLE>");
			Out+=i;
			NextCodec->Len+=i;
			Codec->StreamData=NULL;
		}
		NextCodec->Len-=NextCodec->Codec(NextCodec, NextCodec->Next);
		memmove(NextCodec->Start, Out-NextCodec->Len, NextCodec->Len);
	}

        if (Len >= Codec->Min) {
            return(Len);
        }

        return(Codec->Len);
}

static int
VCard_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	return(Codec->Len);
}

#endif
