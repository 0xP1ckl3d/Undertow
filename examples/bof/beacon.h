#ifndef UNDERTOW_BOF_TEST_BEACON_H
#define UNDERTOW_BOF_TEST_BEACON_H
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <stdint.h>
typedef struct { char *original; char *buffer; int length; int size; } datap;
typedef struct { char *original; char *buffer; int length; int size; } formatp;
void BeaconPrintf(int type, const char *format, ...);
void BeaconOutput(int type, const char *data, int length);
void BeaconDataParse(datap *parser, char *buffer, int size);
int BeaconDataInt(datap *parser);
short BeaconDataShort(datap *parser);
char *BeaconDataExtract(datap *parser, int *size);
int BeaconDataLength(datap *parser);
void BeaconFormatAlloc(formatp *format, int max);
void BeaconFormatReset(formatp *format);
void BeaconFormatFree(formatp *format);
void BeaconFormatAppend(formatp *format, const char *data, int length);
void BeaconFormatPrintf(formatp *format, const char *pattern, ...);
char *BeaconFormatToString(formatp *format, int *size);
void BeaconFormatInt(formatp *format, int value);
#endif
