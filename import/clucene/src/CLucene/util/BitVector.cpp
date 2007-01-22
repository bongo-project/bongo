/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "BitVector.h"

#include "CLucene/store/Directory.h"

CL_NS_USE(store)
CL_NS_DEF(util)

  BitVector::BitVector(int32_t n)
  {
    _count=-1;
    _size = n;
    int32_t len = (_size >> 3) + 1;
    bits = _CL_NEWARRAY(uint8_t,len);
    for ( int32_t i=0;i<len;i++ )
      bits[i] = 0;
  }

  BitVector::~BitVector(){
    _CLDELETE_ARRAY(bits); 
  }

  BitVector::BitVector(Directory* d, const char* name) {
    _count=-1;
    IndexInput* input = d->openInput( name );
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


  void BitVector::set(const int32_t bit) {
    bits[bit >> 3] |= 1 << (bit & 7);
    _count = -1;
  }

  void BitVector::clear(const int32_t bit) {
    bits[bit >> 3] &= ~(1 << (bit & 7));
    _count = -1;
  }

  bool BitVector::get(const int32_t bit) {
    return (bits[bit >> 3] & (1 << (bit & 7))) != 0;
  }

  int32_t BitVector::size() {
    return _size;
  }

  int32_t BitVector::count() {
    // if the vector has been modified
    if (_count == -1) {
      const static uint8_t BYTE_COUNTS[] = {  
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

  void BitVector::write(Directory* d, const char* name) {
    IndexOutput* output = d->createOutput(name);
    try {
      output->writeInt(size());			  // write size
      output->writeInt(count());			  // write count
      output->writeBytes(bits, (_size >> 3) + 1);	  // write bits TODO: CHECK THIS, was bit.length NOT (size >> 3) + 1
    } _CLFINALLY (
        output->close();
        _CLDELETE(output);
      );
  }

CL_NS_END
