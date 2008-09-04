
#include "stored.h"

/**
 * \file Various decoders used as part of the mail process
 */

/**
 * Attempt to decide a given ASCII string according to RFC1432.
 * It does some validation, so it should be clear if a string
 * can be decoded according to those rules.
 * The eventual decoded string is simple utf-8.
 * \param	word	RFC1432_EncodedWord structure we decode into
 * \param	token	string we attempt to decode
 * \return	0 on success, error codes otherwise.
 */

int
EncodedWordInit (RFC1432_EncodedWord *word, char const *token)
{
	char const *ptr, *end_ptr;
	int numq = 0;
	int len;
	
	memset(word, 0, sizeof(RFC1432_EncodedWord));
	if (token == NULL) return 1;
	
	ptr = token;
	while(*ptr != '\0') {
		if (*ptr == '?') {
			numq++;
		}
		// check for non-ASCII characters?
		ptr++;
	}
	// token must be at least this length to be an encoded word
	if (ptr < (token + 4)) return 1;
	// didn't find all the required pieces
	if (numq != 4) return 1;
	
	// must start with =?
	if ((token[0] != '=') || (token[1] != '?')) return 1;
	
	// must end with ?=
	if ((ptr[-1] != '=') || (ptr[-2] != '?')) return 1;
	
	// now we've verified the format, set our various data points.
	ptr = end_ptr = token + 2;
	while(*end_ptr != '?') end_ptr++;
	
	// find the charset specified
	len = end_ptr - ptr;
	word->charset = MemMalloc(len + 1);
	memcpy(word->charset, ptr, len);
	word->charset[len] = '\0';
	
	// set the encoding specified
	end_ptr++;
	switch(*end_ptr) {
		case 'b':
		case 'B':
			word->encoding = EW_ENC_BASE64;
			break;
		case 'q':
		case 'Q':
			word->encoding = EW_ENC_QP;
			break;
		default:
			goto abort;
	}
	
	// find the encoded data
	end_ptr += 2;
	ptr = end_ptr;
	while(*end_ptr != '?') end_ptr++;
	
	len = end_ptr - ptr;
	word->text = MemMalloc(len+1);
	memcpy(word->text, ptr, len);
	word->text[len] = '\0';
	
	// attempt to decode the encoded portion
	if (word->encoding == EW_ENC_BASE64) {
		word->decoded = DecodeBase64(word->text);
	}
	if (word->encoding == EW_ENC_QP) {
		char *d_ptr, *s_ptr;
		
		word->decoded = MemMalloc(len+1);
		d_ptr = word->decoded;
		s_ptr = word->text;
		
		while (*s_ptr != '\0') {
			uint64_t val;
			char save;
			char *p;
			
			switch (*s_ptr) {
				case '=':
					save = s_ptr[3];
					s_ptr[3] = '\0';
					val = HexToUInt64(s_ptr+1, &p);
					*d_ptr = (unsigned char)val;
					s_ptr[3] = save;
					s_ptr += 2;
					d_ptr++;
					break;
				case '_':
					*d_ptr = ' ';
					d_ptr++;
					break;
				default:
					*d_ptr = *s_ptr;
					d_ptr++;
					break;
			}
			s_ptr++;
		}
	}
	
	// FIXME: if charset is not utf-8, we should be converting at this point
	
	return 0;

abort:
	EncodedWordFinish(word);
	return 1;
}

/**
 * Finish with an RFC1432_EncodedWord structure previously set up by
 * EncodedWordInit().
 * \param	word	Structure we wish to destroy
 * \return	Nothing
 */
void
EncodedWordFinish(RFC1432_EncodedWord *word)
{
	if (word->charset) MemFree(word->charset);
	if (word->text) MemFree(word->text);
	if (word->decoded) MemFree(word->decoded);
}
