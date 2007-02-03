/* (C) 2007 Alex Hudson
 * Bongo Project licensing applies to this file, see COPYING
 */
 
#ifndef XPLHASH_H
#define XPLHASH_H

#include <gcrypt.h>

/** \enum
 * Which hash algorithm to use.
 */
typedef enum {
	XPLHASH_MD5 	= GCRY_MD_MD5,	///< Use the MD5 algorithm
	XPLHASH_SHA1	= GCRY_MD_SHA1, ///< Use the SHA1 algorithm
} xpl_hash_type;

/** \enum
 * How to encode the output hash in string form.
 */
typedef enum {
	XPLHASH_UPPERCASE,		///< Use upper-case letters
	XPLHASH_LOWERCASE,		///< Use lower-case letters
} xpl_hash_stringcase;

///< \internal
typedef struct _xpl_hash_context {
	gcry_md_hd_t	gcrypt_context;
	xpl_hash_type	type;
	size_t		buffer_size;
} xpl_hash_context;

#define	XPLHASH_MD5BYTES_LENGTH		16
#define	XPLHASH_SHA1BYTES_LENGTH	20

#define	XPLHASH_MD5_LENGTH		(16*2)+1
#define	XPLHASH_SHA1_LENGTH		(20*2)+1

void XplInit(void);
void XplHashInit(void);

void XplHashNew(xpl_hash_context *ctx, xpl_hash_type type);

void XplHashWrite(xpl_hash_context *context, const void *buffer, size_t length);

void XplHashFinalBytes(xpl_hash_context *context, unsigned char *buffer, size_t length);

void XplHashFinal(xpl_hash_context *context, xpl_hash_stringcase strcase, unsigned char *buffer, size_t length);

void XplRandomData(void *buffer, size_t length);
void XplSaveRandomSeed(void);

#endif // XPLHASH_H
