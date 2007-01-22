/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_util_BitVector_
#define _lucene_util_BitVector_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "CLucene/store/Directory.h"


CL_NS_DEF(util)

/** Optimized implementation of a vector of bits.  This is more-or-less like
  java.util.BitSet, but also includes the following:
  <ul>
  <li>a count() method, which efficiently computes the number of one bits;</li>
  <li>optimized read from and write to disk;</li>
  </ul>

  @author Ben van Klinken
  @version $Id: BitVector.h 2088 2006-05-22 13:32:43Z ustramooner $
  */
	class BitVector:LUCENE_BASE {
	private:
		int32_t _size;
		int32_t _count;

		uint8_t* bits;
	public:
		/// Constructs a vector capable of holding <code>n</code> bits. 
		BitVector(int32_t n);
		/// Destructor for the BitVector
		~BitVector();
		
		/// Constructs a bit vector from the file <code>name</code> in Directory
		///	<code>d</code>, as written by the; method.
		BitVector(CL_NS(store)::Directory* d, const char* name);

		/// Sets the value of <code>bit</code> to one. 
		void set(const int32_t bit);

		/// Sets the value of <code>bit</code> to zero. 
		void clear(const int32_t bit);

		/// Returns <code>true</code> if <code>bit</code> is one and
		///	<code>false</code> if it is zero. 
		bool get(const int32_t bit);

		/// Returns the number of bits in this vector.  This is also one greater than
		///	the number of the largest valid bit number. 
		int32_t size();

		/// Returns the total number of one bits in this vector.  This is efficiently
		///	computed and cached, so that, if the vector is not changed, no
		///	recomputation is done for repeated calls. 
		int32_t count();

		

		/// Writes this vector to the file <code>name</code> in Directory
		///	<code>d</code>, in a format that can be read by the constructor {@link
		///	#BitVector(Directory, String)}.  
		void write(CL_NS(store)::Directory* d, const char* name);

	};
CL_NS_END
#endif
