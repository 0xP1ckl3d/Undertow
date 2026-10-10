#pragma once
#define WIN32_LEAN_AND_MEAN
#include <Windows.h>
#include <stdio.h>
#include <metahost.h>
#include <propvarutil.h>
#pragma comment(lib, "mscoree.lib")
#pragma comment(lib, "Propsys.lib")

#import <mscorlib.tlb> raw_interfaces_only \
    high_property_prefixes("_get","_put","_putref") \
    rename("ReportEvent", "InteropServices_ReportEvent") \
    rename("or", "InteropServices_or")
using namespace mscorlib;

#define APP_DOMAIN L"ShellPower"

// Obfuscated assembly name wrapper function declarations
LPCWSTR GetAssemblyName_SystemCore();
LPCWSTR GetAssemblyName_SystemReflection();
LPCWSTR GetAssemblyName_SystemRuntime();
LPCWSTR GetAssemblyName_PowerShellConsoleHost();
LPCWSTR GetAssemblyName_SystemManagementAutomation();

// Keep as inline function calls for backward compatibility
#define ASSEMBLY_NAME_SYSTEM_CORE GetAssemblyName_SystemCore()
#define ASSEMBLY_NAME_SYSTEM_REFLECTION GetAssemblyName_SystemReflection()
#define ASSEMBLY_NAME_SYSTEM_RUNTIME GetAssemblyName_SystemRuntime()
#define ASSEMBLY_NAME_MICROSOFT_POWERSHELL_CONSOLEHOST GetAssemblyName_PowerShellConsoleHost()
#define ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION GetAssemblyName_SystemManagementAutomation()
#define BINDING_FLAGS_PUBLIC_STATIC mscorlib::BindingFlags::BindingFlags_Public | mscorlib::BindingFlags::BindingFlags_Static
#define BINDING_FLAGS_PUBLIC_INSTANCE mscorlib::BindingFlags::BindingFlags_Public | mscorlib::BindingFlags::BindingFlags_Instance
#define BINDING_FLAGS_NONPUBLIC_STATIC mscorlib::BindingFlags::BindingFlags_NonPublic | mscorlib::BindingFlags::BindingFlags_Static
#define BINDING_FLAGS_NONPUBLIC_INSTANCE mscorlib::BindingFlags::BindingFlags_NonPublic | mscorlib::BindingFlags::BindingFlags_Instance

typedef struct _RT_CONTEXT
{
    ICLRMetaHost* pMetaHost;
    ICLRRuntimeInfo* pRuntimeInfo;
    ICorRuntimeHost* pRuntimeHost;
    IUnknown* pAppDomainThunk;
} RT_CONTEXT, * PRT_CONTEXT;

namespace clr
{
    BOOL clrInitRuntime(PRT_CONTEXT pClrContext, mscorlib::_AppDomain** ppAppDomain);
    void clrDestroyRuntime(PRT_CONTEXT pClrContext, mscorlib::_AppDomain* pAppDomain);
    BOOL clrFindAssembly(LPCWSTR pwszAssemblyName, LPWSTR* ppwszAssemblyPath);
    BOOL clrGetAssembly(mscorlib::_AppDomain* pAppDomain, LPCWSTR pwszAssemblyName, mscorlib::_Assembly** ppAssembly);
    BOOL clrLoadAssembly(mscorlib::_AppDomain* pAppDomain, LPCWSTR pwszAssemblyName, mscorlib::_Assembly** ppAssembly);
    BOOL clrCreateInstance(mscorlib::_AppDomain* pAppDomain, LPCWSTR pwszAssemblyName, LPCWSTR pwszClassName, VARIANT* pvtInstance);
    BOOL clrGetType(mscorlib::_AppDomain* pAppDomain, LPCWSTR pwszAssemblyName, LPCWSTR pwszTypeFullName, mscorlib::_Type** ppType);
    BOOL clrGetProperty(mscorlib::_Type* pType, mscorlib::BindingFlags bindingFlags, LPCWSTR pwszPropertyName, mscorlib::_PropertyInfo** ppPropertyInfo);
    BOOL clrGetPropValue(mscorlib::_Type* pType, mscorlib::BindingFlags bindingFlags, VARIANT vtObject, LPCWSTR pwszPropertyName, VARIANT* pvtPropertyValue);
    BOOL clrGetField(mscorlib::_Type* pType, mscorlib::BindingFlags bindingFlags, LPCWSTR pwszFieldName, mscorlib::_FieldInfo** ppFieldInfo);
    BOOL clrGetFieldValue(mscorlib::_Type* pType, mscorlib::BindingFlags bindingFlags, VARIANT vtObject, LPCWSTR pwszFieldName, VARIANT* pvtFieldValue);
    BOOL clrGetMethod(mscorlib::_Type* pType, mscorlib::BindingFlags bindingFlags, LPCWSTR pwszMethodName, LONG lNbArg, mscorlib::_MethodInfo** ppMethodInfo);
    BOOL clrInvokeMethod(mscorlib::_MethodInfo* pMethodInfo, VARIANT vtObject, SAFEARRAY* pParameters, VARIANT* pvtResult);
    BOOL clrFindMethod(SAFEARRAY* pMethods, LPCWSTR pwszMethodName, LONG lNbArgs, mscorlib::_MethodInfo** ppMethodInfo);
    BOOL clrPrepareMethod(mscorlib::_AppDomain* pAppDomain, VARIANT* pvtMethodHandle);
    BOOL clrGetFuncPtr(mscorlib::_AppDomain* pAppDomain, VARIANT* pvtMethodHandle, PULONG_PTR pFunctionPointer);
    BOOL clrGetJitAddr(mscorlib::_AppDomain* pAppDomain, LPCWSTR pwszAssemblyName, LPCWSTR pwszClassName, LPCWSTR pwszMethodName, DWORD dwNbArgs, PULONG_PTR pMethodAddress);
}

namespace dotnet
{
    BOOL System_Object_GetType(mscorlib::_AppDomain* pAppDomain, VARIANT vtObject, VARIANT* pvtObjectType);
    BOOL System_Type_GetProperty(mscorlib::_AppDomain* pAppDomain, VARIANT vtTypeObject, LPCWSTR pwszPropertyName, VARIANT* pvtPropertyInfo);
    BOOL System_Reflection_PropertyInfo_GetValue(mscorlib::_AppDomain* pAppDomain, VARIANT vtPropertyInfo, VARIANT vtObject, SAFEARRAY* pIndex, VARIANT* pvtValue);
    BOOL System_Reflection_PropertyInfo_GetValue(mscorlib::_AppDomain* pAppDomain, VARIANT vtPropertyInfo, VARIANT vtObject, VARIANT* pvtValue);
}

