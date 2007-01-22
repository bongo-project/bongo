/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_util_BitSet_
#define _lucene_util_BitSet_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "CLucene/store/Directory.h"

CL_NS_DEF(util)
class BitSet:LUCENE_BASE {
	int32_t _size;
	int32_t _count;
	uint8_t *bits;
	
protected:
	BitSet( const BitSet& copy ) :
		_size( copy._size ),
		_count(-1)
	{
		int32_t len = (_size >> 3) + 1;
		bits = _CL_NEWARRAY(uint8_t, len);
		memcpy( bits, copy.bits, len );
	}

public:
	///Create a bitset with the specified size
	BitSet ( int32_t size ):
	  _size(size),
		_count(-1)
	{
		int32_t len = (_size >> 3) + 1;
		bits = _CL_NEWARRAY(uint8_t, len);
		memset(bits,0,len);
	}
	BitSet(CL_NS(store)::Directory* d, const char* name) {
		_count=-1;
		CL_NS(store)::IndexInput* input = d->openInput( name );
		try {
			_size = input->readInt();			  // read size
		  	_count = input->readInt();			  // read count
	
			bits = _CL_NEWARRAY(uint8_t,(_size >> 3) + 1);		  // allocate bits
			input->readBytes(bits, 0, (_size >> 3) + 1);	  // read bits
		} _CLFINALLY (
		    input->close();
		    _CLDELETE(input );
		);
	}
	
	void write(CL_NS(store)::Directory* d, const char* name) {
		CL_NS(store)::IndexOutput* output = d->createOutput(name);
		try {
		  output->writeInt(size());			  // write size
		  output->writeInt(count());			  // write count
		  output->writeBytes(bits, (_size >> 3) + 1);	  // write bits
		} _CLFINALLY (
		    output->close();
		    _CLDELETE(output);
		);
	}

	
	///Destructor for the bit set
	~BitSet(){
		_CLDELETE_ARRAY(bits);
	}
	
	///get the value of the specified bit
	inline bool get(const int32_t bit) const{
	    return (bits[bit >> 3] & (1 << (bit & 7))) != 0;
	}
	
	///set the value of the specified bit
	void set(const int32_t bit, bool val=true){
		_count = -1;
	
		if (val)
			bits[bit >> 3] |= 1 << (bit & 7);
		else
			bits[bit >> 3] &= ~(1 << (bit & 7));
		_count =-1;
	}
	
	///returns the size of the bitset
	int32_t size() const {
	  return _size;
	}
	
	/// Returns the total number of one bits in this BitSet.  This is efficiently
	///	computed and cached, so that, if the BitSet is not changed, no
	///	recomputation is done for repeated calls. 
	int32_t count(){
		// if the BitSet has been modified
	    if (_count == -1) {
	      static const uint8_t BYTE_COUNTS[] = {  
	            0, 1, 1, 2, 1, 2, 2, 3, 1, 2, 2, 3, 2, 3, 3, 4,
	            1, 2, 2, 3, 2, 3, 3, 4, 2, 3, 3, 4, 3, 4, 4, 5,
	            1, 2, 2, 3, 2, 3, 3, 4, 2, 3, 3, 4, 3, 4, 4, 5,
	            2, 3, 3, 4, 3, 4, 4, 5, 3, 4, 4, 5, 4, 5, 5, 6,
	            1, 2, 2, 3, 2, 3, 3, 4, 2, 3, 3, 4, 3, 4, 4, 5,
	            2, 3, 3, 4, 3, 4, 4, 5, 3, 4, 4, 5, 4, 5, 5, 6,
	            2, 3, 3, 4, 3, 4, 4, 5, 3, 4, 4, 5, 4, 5, 5, 6,
	            3, 4, 4, 5, 4, 5, 5, 6, 4, 5, 5, 6, 5, 6, 6, 7,
	            1, 2, 2, 3, 2, 3, 3, 4, 2, 3, 3, 4, 3, 4, 4, 5,
	            2, 3, 3, 4, 3, 4, 4, 5, 3, 4, 4, 5, 4, 5, 5, 6,
	            2, 3, 3, 4, 3, 4, 4, 5, 3, 4, 4, 5, 4, 5, 5, 6,
	            3, 4, 4, 5, 4, 5, 5, 6, 4, 5, 5, 6, 5, 6, 6, 7,
	            2, 3, 3, 4, 3, 4, 4, 5, 3, 4, 4, 5, 4, 5, 5, 6,
	            3, 4, 4, 5, 4, 5, 5, 6, 4, 5, 5, 6, 5, 6, 6, 7,
	            3, 4, 4, 5, 4, 5, 5, 6, 4, 5, 5, 6, 5, 6, 6, 7,
	            4, 5, 5, 6, 5, 6, 6, 7, 5, 6, 6, 7, 6, 7, 7, 8};
	
	      int32_t c = 0;
	      int32_t end = (_size >> 3) + 1;
	      for (int32_t i = 0; i < end; i++)
	        c += BYTE_COUNTS[bits[i] & 0xFF];	  // sum bits per uint8_t
	      _count = c;
	    }
	    return _count;
	}
	BitSet *clone() const{
		return _CLNEW BitSet( *this );
	}
};


CL_NS_END
#endif
